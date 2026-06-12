package main

import (
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/config"
	"github.com/justinlindh/digits/pi/digitsd/internal/devmode"
	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	"github.com/justinlindh/digits/pi/digitsd/internal/version"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
)

// signalController is the subset of *phone.Controller that the signaling
// dispatch drives. Holding it behind an interface lets dispatch routing be
// exercised in tests with a recording fake, without standing up a real
// serial port or audio path. *phone.Controller satisfies it.
type signalController interface {
	HandleSignal(msgType, sender string)
	State() phone.State
	SetCallReturnNumber(number string)
	ResetToDialtone()
	HandleCallReturnRing(target string)
	HandleConferenceMember(confID string, members []sigclient.ConferenceMemberInfo)
	HandleConferenceConnect(confID, peer string, initiator bool)
	HandleConferenceLeave(confID, peer, reason string)
	HandleConferenceEnd(confID, reason string)
	HandleConferenceRejected(confID, reason string)
}

// Compile-time check that the real controller satisfies the dispatch's view
// of it, so a future signature drift fails here rather than only at the
// cb.ctrlSignal assignment in main().
var _ signalController = (*phone.Controller)(nil)

// handleSignal routes a single inbound signaling message to its handler.
// It is the body of what used to be the inline switch in main()'s event
// loop: same case ordering, same locking, same logging. The loop now reads a
// message and calls this method.
//
// Dependencies that the handlers need but that are owned by the run loop
// (serial port, signaling client, mixer, controller, the firmware-update
// closures, the pairing-refresh timer) are stored on daemonCallbacks and
// wired once before the loop starts.
func (d *daemonCallbacks) handleSignal(msg *sigclient.Message) {
	ctrl := d.ctrlSignal
	mixer := d.mixer
	sp := d.serial

	slog.Info("signal rx", "type", msg.Type, "from", msg.From)
	switch msg.Type {
	case sigclient.TypeRing:
		d.mu.Lock()
		d.pendingCaller = msg.From
		d.mu.Unlock()
		ctrl.HandleSignal("ring", "")
	case sigclient.TypeAnswer:
		// Set remote description from the answer SDP before poking the FSM.
		d.mu.Lock()
		if d.peerMgr != nil && msg.SDP != "" {
			if err := d.peerMgr.SetAnswer(msg.SDP); err != nil {
				slog.Error("webrtc: set answer failed", "error", err)
			} else {
				slog.Info("webrtc: set remote answer", "from", msg.From, "bytes", len(msg.SDP))
			}
		}
		d.mu.Unlock()
		ctrl.HandleSignal("answer", msg.From)
	case sigclient.TypeHangup:
		ctrl.HandleSignal("hangup", msg.From)
	case sigclient.TypeBusy:
		if d.callReturnOrigin.Load() {
			d.callReturnOrigin.Store(false)
			target := msg.From
			slog.Info("call_return: target busy, registering retry", "target", target)
			ctrl.HandleSignal("busy", msg.From)
			go func() {
				time.Sleep(500 * time.Millisecond)
				if ctrl.State() != phone.StateCALLING {
					return
				}
				mixer.StopTone()
				mixer.PlayOnce("call_return_retry")
				sendSignal(d.currentSig(), &sigclient.Message{
					Type:   sigclient.TypeCallReturnRetry,
					Number: target,
				})
			}()
		} else {
			ctrl.HandleSignal("busy", msg.From)
		}
	case sigclient.TypeDTMF:
		// Remote peer pressed a digit during the call. Play the local
		// DTMF sample so the user hears what their peer is pressing,
		// matching real-phone behavior.
		if ctrl.State() != phone.StateCONNECTED {
			slog.Debug("dtmf: ignoring (not connected)", "from", msg.From)
			break
		}
		d.mu.Lock()
		peer := d.callPeer
		d.mu.Unlock()
		if msg.From != peer {
			slog.Debug("dtmf: ignoring (wrong peer)", "from", msg.From, "expected", peer)
			break
		}
		if msg.Digit == "" {
			slog.Warn("dtmf: empty digit in message")
			break
		}
		dtmfName := dtmfToneName(msg.Digit)
		if dtmfName == "" {
			slog.Warn("dtmf: unrecognized digit", "digit", msg.Digit)
			break
		}
		mixer.PlayOnce(dtmfName)
	case sigclient.TypeError:
		slog.Warn("signal error", "error", msg.Error)
		// ADD_CALLING: route through the controller so state transitions
		// to ADD_INTERCEPT and the added peer is torn down. The user
		// flashes to return to the held party.
		if ctrl.State() == phone.StateADD_CALLING {
			ctrl.HandleSignal("error", msg.From)
			break
		}
		// 2-party CALLING: emulate real phone -- ringback -> SIT -> busy
		go func() {
			// 1. Brief silence (call setup delay, ~1s)
			time.Sleep(1 * time.Second)
			if ctrl.State() != phone.StateCALLING {
				return
			}
			// 2. Ringback for ~8s (simulates 1-2 rings)
			slog.Info("playing ringback (number unreachable)")
			mixer.PlayLoop("tone_ringback")
			time.Sleep(8 * time.Second)
			if ctrl.State() != phone.StateCALLING {
				return
			}
			// 3. SIT tones + "number not in service" announcement
			slog.Info("playing disconnected announcement")
			mixer.StopTone()
			mixer.PlayOnce("disconnected")
			// Wait for announcement to finish (poll rather than guess duration)
			for mixer.OncePlaying() {
				time.Sleep(200 * time.Millisecond)
				if ctrl.State() != phone.StateCALLING {
					return
				}
			}
			// 4. Brief silence, then reorder tone (fast busy) until hang-up
			time.Sleep(500 * time.Millisecond)
			if ctrl.State() != phone.StateCALLING {
				return
			}
			slog.Info("playing reorder tone")
			mixer.PlayLoop("tone_busy")
		}()
	case sigclient.TypeSDP:
		if msg.ConfID != "" {
			// Conference SDP: route to the mesh peer for this member.
			d.mu.Lock()
			mesh := d.mesh
			d.mu.Unlock()

			if mesh == nil || mesh.GetPeer(msg.From) == nil {
				// No peer yet: we are the responder receiving the initiator's offer.
				answerSDP, err := d.setupMeshResponder(msg.From, msg.SDP, msg.ConfID)
				if err != nil {
					slog.Error("conference: setupMeshResponder failed", "from", msg.From, "err", err)
					break
				}
				sendSignal(d.currentSig(), &sigclient.Message{
					Type:   sigclient.TypeSDP,
					To:     msg.From,
					ConfID: msg.ConfID,
					SDP:    answerSDP,
				})
				slog.Info("conference: sent SDP answer to initiator", "to", msg.From, "conf_id", msg.ConfID)
			} else {
				// Peer already exists: we were the initiator and this is the answer.
				if err := mesh.GetPeer(msg.From).SetAnswer(msg.SDP); err != nil {
					slog.Error("conference: set answer failed", "from", msg.From, "err", err)
				} else {
					slog.Info("conference: applied SDP answer from peer", "from", msg.From)
				}
			}
			break
		}
		d.mu.Lock()
		switch {
		case d.peerMgr == nil:
			// Incoming call: offer arrived before we've answered.
			// Stash it for AnswerCall to pick up.
			d.pendingOffer = msg.SDP
			if d.pendingCaller == "" && msg.From != "" {
				d.pendingCaller = msg.From
				slog.Info("set pendingCaller from SDP", "from", msg.From)
			}
			slog.Info("stored pending SDP offer", "from", msg.From, "bytes", len(msg.SDP))
			d.prepareAnswer()
		case d.isRestartingICE:
			// Mid-call: the only legitimate reason to receive an SDP
			// with an active peerMgr is the restart-answer we asked
			// for when we initiated an ICE restart.
			if err := d.peerMgr.SetAnswer(msg.SDP); err != nil {
				slog.Error("webrtc: set restart answer failed", "error", err)
			} else {
				slog.Info("webrtc: applied restart answer", "from", msg.From, "bytes", len(msg.SDP))
			}
		default:
			slog.Warn("webrtc: unexpected SDP with active peer, ignoring", "from", msg.From, "bytes", len(msg.SDP))
		}
		d.mu.Unlock()
	case sigclient.TypeICE:
		if msg.ConfID != "" {
			// Conference ICE: route to the mesh peer for this member.
			d.mu.Lock()
			mesh := d.mesh
			d.mu.Unlock()
			if mesh == nil {
				slog.Warn("conference: ICE candidate before mesh initialized", "from", msg.From)
				break
			}
			pm := mesh.GetPeer(msg.From)
			if pm == nil {
				slog.Warn("conference: ICE candidate before peer created", "from", msg.From)
				break
			}
			if err := pm.AddICECandidate(msg.Candidate); err != nil {
				slog.Error("conference: add ICE candidate failed", "from", msg.From, "err", err)
			}
			break
		}
		d.mu.Lock()
		if d.peerMgr != nil {
			if err := d.peerMgr.AddICECandidate(msg.Candidate); err != nil {
				slog.Warn("webrtc: add ICE candidate failed", "error", err)
			}
		} else if d.preAnswer.peerMgr != nil {
			if err := d.preAnswer.peerMgr.AddICECandidate(msg.Candidate); err != nil {
				slog.Warn("webrtc: add ICE candidate to preAnswer failed", "error", err)
			}
		} else {
			d.pendingICE = append(d.pendingICE, msg.Candidate)
			slog.Info("queued ICE candidate (peerMgr not ready)", "total_queued", len(d.pendingICE))
		}
		d.mu.Unlock()
	case sigclient.TypeUpdateTrigger:
		slog.Info("signal: received update trigger from server", "target_pi", msg.TargetPiVersion, "target_fw", msg.TargetFWVersion)
		statusReporter := func(status, detail string) {
			sendSignal(d.currentSig(), &sigclient.Message{
				Type:         sigclient.TypeUpdateStatus,
				UpdateStatus: status,
				UpdateDetail: detail,
			})
		}
		fwVersion, _ := d.getFirmwareVersion()
		go runTargetedUpdate(d.serverURL, version.Version, fwVersion,
			msg.TargetPiVersion, msg.TargetFWVersion, d.flashCapable.Load(), statusReporter, d.requeryFirmware)

	case sigclient.TypeReleaseAvailable:
		slog.Info("signal: release_available", "pi", msg.LatestPiVersion, "fw", msg.LatestFWVersion)
		if d.autoUpdateEnabled.Load() && !devmode.SkipAutoUpdate(devmode.DefaultSkipAutoUpdatePath) {
			go d.triggerAutoUpdate()
		}

	case sigclient.TypeFactoryReset:
		slog.Info("factory reset: triggered by server")
		go triggerFactoryReset(d.currentSig(), d.deviceID)

	case sigclient.TypeICERestart:
		d.mu.Lock()
		pm := d.peerMgr
		peer := d.callPeer
		d.mu.Unlock()
		if pm == nil {
			slog.Info("ice-restart: no active peer connection, ignoring")
			break
		}
		slog.Info("ice-restart: received restart offer", "from", msg.From, "bytes", len(msg.SDP))
		answerSDP, err := pm.AcceptOffer(msg.SDP)
		if err != nil {
			slog.Error("ice-restart: accept offer failed", "error", err)
			break
		}
		d.mu.Lock()
		d.isRestartingICE = true
		d.cancelRestartTimerLocked()
		d.startRestartTimeout()
		d.mu.Unlock()
		if peer == "" {
			peer = msg.From
		}
		slog.Info("ice-restart: sending restart answer", "peer", peer, "bytes", len(answerSDP))
		sendSignal(d.currentSig(), &sigclient.Message{
			Type: sigclient.TypeSDP,
			To:   peer,
			SDP:  answerSDP,
		})

	case sigclient.TypeICEServers:
		d.mu.Lock()
		d.iceServers = nil
		for _, s := range msg.Servers {
			d.iceServers = append(d.iceServers, owebrtc.ICEServerConfig{
				URLs:       s.URLs,
				Username:   s.Username,
				Credential: s.Credential,
			})
		}
		// Push fresh creds into the live 2-party PeerConnection so an
		// ICE restart triggered after the TURN TTL (2h) uses valid creds.
		// Mesh peers are not updated here; see PeerManager.UpdateICEServers.
		pm := d.peerMgr
		servers := d.iceServers
		d.mu.Unlock()
		if pm != nil {
			if err := pm.UpdateICEServers(servers); err != nil {
				slog.Warn("ice: failed to update live peer connection", "error", err)
			} else {
				slog.Info("ice: updated live peer connection with fresh servers")
			}
		}
		slog.Info("ice: cached servers from signald", "count", len(msg.Servers))

	case sigclient.TypePairingCode:
		d.pairingCode = msg.PairingCode
		refresh := pairingRefreshInterval
		if msg.PairingCodeTTL > 0 {
			ttl := time.Duration(msg.PairingCodeTTL) * time.Second
			d.pairingCodeExpiresAt = time.Now().Add(ttl)
			// Refresh a margin before expiry so the announced code is
			// still valid while a user types it. Guard against a TTL
			// shorter than the margin.
			if dl := ttl - pairingRefreshMargin; dl > 0 {
				refresh = dl
			} else {
				refresh = ttl / 2
			}
		} else {
			// Older server without a TTL: fall back to the fixed cadence.
			d.pairingCodeExpiresAt = time.Now().Add(pairingRefreshInterval)
		}
		slog.Info("PAIRING REQUIRED: pick up handset to hear it", "code", msg.PairingCode, "ttl_s", msg.PairingCodeTTL)
		d.pairingRefresh.Reset(refresh)

	case sigclient.TypePaired:
		d.pairingRefresh.Stop()
		if msg.DeviceToken != "" && d.cfg != nil {
			d.cfg.DeviceToken = msg.DeviceToken
			d.cfg.PairingCode = ""
			if msg.Number != "" {
				d.cfg.PhoneNumber = msg.Number
				d.number = msg.Number
			}
			if err := d.cfg.Save(); err != nil {
				slog.Warn("signal: paired -- failed to save config", "error", err)
			} else {
				slog.Info("signal: paired", "number", msg.Number, "config", d.cfg.Path())
			}
			d.paired.Store(true)
			d.pairingCode = ""
			sp.StateSet("PAIRED")
			mixer.StopAll()
			sp.SendFire("TONE:DIAL")
			slog.Info("signal: restarting to register", "number", msg.Number)
			go func() {
				time.Sleep(1 * time.Second)
				os.Exit(0) // systemd restarts; Pico tone survives
			}()
		}

	case sigclient.TypeRestart:
		mode := msg.RestartMode
		slog.Info("received restart command", "mode", mode)
		switch mode {
		case "service":
			sendSignal(d.currentSig(), &sigclient.Message{
				Type:         sigclient.TypeUpdateStatus,
				UpdateStatus: "restarting",
				UpdateDetail: "Service restart requested",
			})
			go func() {
				time.Sleep(500 * time.Millisecond)
				slog.Info("restarting service via exit (systemd will restart)")
				os.Exit(0)
			}()
		case "reboot":
			sendSignal(d.currentSig(), &sigclient.Message{
				Type:         sigclient.TypeUpdateStatus,
				UpdateStatus: "rebooting",
				UpdateDetail: "Device reboot requested",
			})
			go func() {
				time.Sleep(500 * time.Millisecond)
				slog.Info("rebooting device")
				if err := exec.Command("sudo", "reboot").Run(); err != nil {
					slog.Error("reboot command failed", "err", err)
				}
			}()
		default:
			slog.Warn("unknown restart mode", "mode", mode)
		}

	case sigclient.TypeRingTest:
		slog.Info("ring test: triggering 1s bell")
		sp.SendFire("RING:TEST")
		go func() {
			time.Sleep(1 * time.Second)
			sp.SendFire("RING:STOP")
			slog.Info("ring test: stopped")
		}()

	case sigclient.TypeDevMode:
		enable := msg.DevMode
		password := msg.DevModePassword
		slog.Info("dev mode: command received", "enable", enable)
		if d.devMode == nil {
			slog.Warn("dev mode: manager unavailable, ignoring command")
			break
		}
		go func() {
			var err error
			if enable {
				err = d.devMode.Enable(password)
			} else {
				err = d.devMode.Disable()
			}
			if err != nil {
				slog.Error("dev mode: apply failed", "enable", enable, "error", err)
				return
			}
			// Re-report device_info so the server reflects the new state;
			// sendDeviceInfo reads the flag the helper just wrote.
			fwVer, fwCom := d.getFirmwareVersion()
			sendDeviceInfo(d.currentSig(), fwVer, fwCom)
		}()

	case sigclient.TypeLineSettings:
		if msg.LineSettings == nil {
			slog.Warn("line_settings message missing payload", "from", msg.From)
			break
		}

		style := msg.LineSettings.VoiceStyle
		if style == "" {
			style = config.VoiceStyleCopper
		}
		d.mu.Lock()
		currentStyle := d.cfg.VoiceStyle
		currentSilent := d.cfg.SilentMode
		d.mu.Unlock()

		if style != currentStyle {
			slog.Info("line_settings applied", "voice_style", style)
			d.applyVoiceStyleLive(style)
			if err := d.setVoiceStyleConfig(style); err != nil {
				slog.Warn("line_settings: voice-style save failed", "err", err)
			}
		}

		silent := msg.LineSettings.SilentMode
		if silent != currentSilent {
			slog.Info("line_settings applied", "silent_mode", silent)
			d.applySilentModeLive(silent)
			if err := d.setSilentModeConfig(silent); err != nil {
				slog.Warn("line_settings: silent-mode save failed", "err", err)
			}
		}

		au := msg.LineSettings.AutoUpdate
		if devmode.SkipAutoUpdate(devmode.DefaultSkipAutoUpdatePath) {
			slog.Info("line_settings: ignoring server auto_update push (dev-mode skip flag)", "server_wants", au)
		} else if au != d.autoUpdateEnabled.Load() {
			d.autoUpdateEnabled.Store(au)
			slog.Info("line_settings applied", "auto_update", au)
			if err := d.setAutoUpdateConfig(au); err != nil {
				slog.Warn("line_settings: auto-update save failed", "err", err)
			}
			if au && d.triggerAutoUpdate != nil {
				go d.triggerAutoUpdate()
			}
		}

		if vm := msg.LineSettings.Voicemail; vm != nil {
			target := config.Voicemail{
				Enabled:     vm.Enabled,
				RingTimeout: time.Duration(vm.RingTimeoutSeconds) * time.Second,
			}
			d.mu.Lock()
			current := d.cfg.Voicemail
			d.mu.Unlock()

			if target != current {
				if target.Enabled != current.Enabled {
					slog.Info("line_settings applied", "voicemail_enabled", target.Enabled)
				}
				if target.RingTimeout != current.RingTimeout {
					slog.Info("line_settings applied", "voicemail_ring_timeout", target.RingTimeout)
				}
				if err := d.setVoicemailConfig(target); err != nil {
					slog.Warn("line_settings: voicemail save failed", "err", err)
				}
			}
		}

	case sigclient.TypeConferenceMember:
		ctrl.HandleConferenceMember(msg.ConfID, msg.Members)
	case sigclient.TypeConferenceConnect:
		ctrl.HandleConferenceConnect(msg.ConfID, msg.Peer, msg.Initiator)
	case sigclient.TypeConferenceLeave:
		ctrl.HandleConferenceLeave(msg.ConfID, msg.Peer, msg.Reason)
	case sigclient.TypeConferenceEnd:
		ctrl.HandleConferenceEnd(msg.ConfID, msg.Reason)
	case sigclient.TypeConferenceRejected:
		ctrl.HandleConferenceRejected(msg.ConfID, msg.Reason)

	case sigclient.TypeCallReturnResult:
		number := msg.Number
		if number == "" {
			slog.Info("call_return: no calls available")
			mixer.PlayOnce("call_return_none")
			go func() {
				time.Sleep(3 * time.Second)
				if ctrl.State() != phone.StateCALL_RETURN {
					return
				}
				ctrl.ResetToDialtone()
				mixer.PlayLoop("tone_dial")
			}()
		} else {
			slog.Info("call_return: announcing last caller", "number", number)
			ctrl.SetCallReturnNumber(number)
			mixer.PlayOnce("call_return_prefix")
			for _, ch := range number {
				mixer.PlayOnce("spoken_" + string(ch))
			}
			mixer.PlayOnce("call_return_suffix")
		}

	case sigclient.TypeCallReturnRing:
		target := msg.Number
		slog.Info("call_return: target free, ringing", "target", target)
		ctrl.HandleCallReturnRing(target)

	case sigclient.TypeCallReturnCancelled:
		slog.Info("call_return: retry cancelled by server")
		mixer.PlayOnce("call_return_cancel")
		go func() {
			time.Sleep(3 * time.Second)
			if ctrl.State() != phone.StateCALL_RETURN {
				return
			}
			ctrl.ResetToDialtone()
			mixer.PlayLoop("tone_dial")
		}()

	default:
		slog.Warn("signal: unhandled message type", "type", msg.Type)
	}
}
