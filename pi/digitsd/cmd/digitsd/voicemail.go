package main

// Voicemail callbacks and helpers. Cut from main.go to keep the daemon
// entrypoint focused on startup and shared infrastructure. The
// daemonCallbacks struct and the voicemail-related fields on it (store,
// recorder, voicemailMu, ...) remain on main.go: only the methods that
// operate on those fields live here.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	"github.com/justinlindh/digits/pi/digitsd/internal/codec"
	"github.com/justinlindh/digits/pi/digitsd/internal/config"
	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	"github.com/justinlindh/digits/pi/digitsd/internal/voicemail"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"

	"github.com/pion/webrtc/v4"
)

// playAnnouncementSequence plays multiple one-shot tones in sequence, waiting
// for each to finish before starting the next. Blocks the calling goroutine
// for the full duration. Never call while holding voicemailMu or d.mu.
func (d *daemonCallbacks) playAnnouncementSequence(tones ...string) {
	for _, name := range tones {
		d.mixer.PlayOnce(name)
		// 10s is far longer than any prompt clip; it bounds the wait so a
		// stalled mixer can never wedge the calling goroutine forever.
		waitForOnceComplete(d.mixer, 10*time.Second)
		time.Sleep(30 * time.Millisecond)
	}
}

// announceMessageCount composes "You have N new message(s)" from individual
// clips. Counts above 9 get a single self-contained "lost count" phrase
// since there is no clip to voice the exact number.
func (d *daemonCallbacks) announceMessageCount(count int) {
	if count <= 0 {
		d.playAnnouncementSequence("vm_no_messages")
		return
	}
	if count > 9 {
		d.playAnnouncementSequence("vm_lost_count")
		return
	}

	seq := []string{"vm_you_have", fmt.Sprintf("spoken_%d", count)}
	if count == 1 {
		seq = append(seq, "vm_new_message")
	} else {
		seq = append(seq, "vm_new_messages")
	}
	d.playAnnouncementSequence(seq...)
}

// messageNumberClips returns the clip sequence for the spoken "Message N"
// announcement, composed as vm_message + spoken_N the same way
// announceMessageCount composes its phrase. A non-positive number yields no
// clips. Positions above 9 have no digit clip, so only the bare "message"
// word is returned as a separator cue.
func messageNumberClips(number int) []string {
	if number <= 0 {
		return nil
	}
	if number > 9 {
		return []string{"vm_message"}
	}
	return []string{"vm_message", fmt.Sprintf("spoken_%d", number)}
}

// announceMessageNumber speaks "Message N" before a message plays during a
// *98 retrieval session. It is only used when the session holds two or more
// messages; a lone message is already identified by the "you have 1 message"
// count intro.
func (d *daemonCallbacks) announceMessageNumber(number int) {
	clips := messageNumberClips(number)
	if len(clips) == 0 {
		return
	}
	d.playAnnouncementSequence(clips...)
}

// savedCountClips returns the clip sequence for "You have N saved message(s)",
// announced when *98 retrieval crosses from the new messages into the saved
// ones. It mirrors announceMessageCount: a non-positive count yields no clips,
// and a count above 9 falls back to the self-contained "lost count" phrase
// since there is no clip to voice the exact number.
func savedCountClips(count int) []string {
	if count <= 0 {
		return nil
	}
	if count > 9 {
		return []string{"vm_lost_count"}
	}
	seq := []string{"vm_you_have", fmt.Sprintf("spoken_%d", count)}
	if count == 1 {
		return append(seq, "vm_saved_message")
	}
	return append(seq, "vm_saved_messages")
}

// announceSavedCount speaks "You have N saved messages" at the transition
// from the new-message phase into the saved-message review phase.
func (d *daemonCallbacks) announceSavedCount(count int) {
	clips := savedCountClips(count)
	if len(clips) == 0 {
		return
	}
	d.playAnnouncementSequence(clips...)
}

// voicemailPlaybackSession bundles the cancelable context, message ID, and
// open Player for one retrieval-playback message. Each DTMF dispatch
// (delete, skip, replay, mark-heard) tears down the current session and
// either opens the next message or transitions out of playback.
type voicemailPlaybackSession struct {
	ctx    context.Context
	cancel context.CancelFunc
	id     int64
	number int  // 1-based position in the retrieval session, for the "Message N" announcement
	saved  bool // true for a saved-message-review session; false during the new-message phase
	player *voicemail.Player
}

// ledModeWithVoicemailHint returns "SLOWER_PULSE" instead of "OFF" when
// voicemail is enabled and at least one message is unheard; otherwise
// returns mode unchanged. Shared by SendLED (controller-driven
// transitions) and evaluateLED (background mutations that the controller
// would not otherwise re-emit).
//
// UnheardCount() reads ~50 small files. On the SLOWER_PULSE path the
// file system traffic happens every time the controller transitions to
// idle, which is not a hot path. Cache later if profiling shows it.
func (d *daemonCallbacks) ledModeWithVoicemailHint(mode string) string {
	if mode != "OFF" {
		return mode
	}
	d.mu.Lock()
	store := d.voicemailStore
	enabled := d.cfg != nil && d.cfg.Voicemail.Enabled
	d.mu.Unlock()
	if store == nil || !enabled {
		return mode
	}
	n, err := store.UnheardCount()
	if err != nil {
		slog.Warn("voicemail: unheard count failed, leaving LED off", "error", err)
		return mode
	}
	if n > 0 {
		return "SLOWER_PULSE"
	}
	return mode
}

// VoicemailPickup is invoked by the controller when the homeowner picks up
// the handset during a voicemail recording. The partial recording is finalized
// (any audio captured before the lift is saved as a regular message), then the
// caller's decoded PCM stream is bridged into the mixer so it plays through
// the earpiece. The OnRemoteTrack goroutine from VoicemailAutoAnswer is
// already decoding and feeding voicemailWebRTCCh; once the mixer registers it
// as an active source, audio flows immediately.
func (d *daemonCallbacks) VoicemailPickup() {
	d.mu.Lock()
	defer d.mu.Unlock()

	var savedPartial bool
	d.recorderMu.Lock()
	if d.recorder != nil {
		if msg, err := d.recorder.Finalize(); err != nil {
			slog.Error("voicemail pickup: finalize failed", "peer", d.callPeer, "error", err)
		} else if msg.ID != 0 {
			slog.Info("voicemail pickup: saved partial recording", "peer", d.callPeer, "id", msg.ID, "duration", msg.Duration)
			savedPartial = true
		}
		d.recorder = nil
	}
	d.recorderMu.Unlock()

	// Fired off the main goroutine so it can take d.mu inside
	// publishVoicemailState once this function's defer Unlock runs.
	if savedPartial {
		go d.publishVoicemailState()
	}

	if d.callPeer == "" {
		slog.Warn("voicemail pickup: no call peer")
		return
	}
	if d.voicemailWebRTCCh == nil {
		slog.Warn("voicemail pickup: no decoded audio channel", "peer", d.callPeer)
		return
	}

	d.mixer.ImportWebRTCSource(d.callPeer, d.voicemailWebRTCCh)
	d.voicemailWebRTCCh = nil

	// VoicemailAutoAnswer mutes the local mic just before transitioning into
	// recording so the caller's outbound stream is DTX comfort noise instead
	// of room audio. On pickup we are bridging back to a live two-way call,
	// so the mute has to come back off or the caller can't hear the
	// homeowner. Holding d.mu is safe; SetMuted is its own atomic.
	if d.pipeline != nil {
		d.pipeline.SetMuted(false)
	}

	slog.Info("voicemail pickup: bridged to live call", "peer", d.callPeer)
}

// VoicemailAutoAnswer completes the SDP/ICE handshake for an unanswered call,
// opens a voicemail recorder, brings up the audio pipeline, and plays the
// outgoing greeting followed by the prompt beep. It mirrors the slow path of
// AnswerCall but diverges in three ways:
//
//  1. No mixer.AddWebRTCSource: caller audio does not play through the
//     earpiece during voicemail. Decoded PCM is sent to voicemailWebRTCCh
//     instead, which VoicemailPickup later hands to the mixer if the
//     homeowner picks up mid-recording.
//  2. OnRemoteTrack runs the same three-phase startup as the live call path
//     (discard until pipeline ready, drain to live, then feed) but in the
//     live phase ALSO tees the raw Opus payload into the recorder.
//  3. After SDP exchange and pipeline start, a small goroutine plays the
//     outgoing greeting (custom .frames if recorded, otherwise the embedded
//     default WAV) followed by the prompt beep, then mutes the outbound mic
//     and transitions the controller into the recording state.
func (d *daemonCallbacks) VoicemailAutoAnswer() {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Snapshot caller up front so the early-return logs and the OnRemoteTrack
	// closure all carry the identifier.
	caller := d.pendingCaller

	if d.pendingOffer == "" {
		slog.Warn("voicemail: no pending offer, aborting auto-answer", "caller", caller)
		return
	}
	if d.voicemailStore == nil {
		slog.Warn("voicemail: store not available, aborting auto-answer", "caller", caller)
		return
	}

	t0 := time.Now()
	d.mixer.StopTone()

	offerSDP := d.pendingOffer
	d.pendingOffer = ""
	d.pendingCaller = ""

	d.callPeer = caller
	d.isCaller = false
	d.isRestartingICE = false

	iceCfg := owebrtc.NewICEConfig(d.iceServers)
	var err error
	d.peerMgr, err = owebrtc.NewPeerManager(iceCfg)
	if err != nil {
		slog.Error("voicemail: new peer manager failed", "caller", caller, "error", err)
		return
	}

	vmPM := d.peerMgr
	d.peerMgr.OnConnectionState = func(state webrtc.PeerConnectionState) {
		d.handleConnectionStateChange(vmPM, state)
	}

	// Decoded PCM target. Buffered so brief mixer-attach delays during
	// pickup do not block the decode loop; overflow is dropped via the
	// non-blocking select below, same as the live-call path. VoicemailPickup
	// claims this channel and hands it to the mixer when the caller is
	// answered mid-recording.
	webrtcCh := make(chan []int16, 8)
	d.voicemailWebRTCCh = webrtcCh

	// Recording stays disarmed until the greeting and beep finish. The
	// remote track goes live well before then, so an early tee would
	// prepend the greeting duration of caller-side silence to the message.
	d.voicemailRecordArmed.Store(false)

	d.peerMgr.OnRemoteTrack = func(track *webrtc.TrackRemote) {
		go func() {
			defer recoverGoroutine("voicemail-record")
			var frameCount int

			// Phase 1: wait for the audio pipeline to come up. pm.Decode is
			// still called to keep the Opus decoder's internal state in sync;
			// raw payloads are NOT teed into the recorder yet because the
			// recorder may not be open until BeginRecording returns below.
			slog.Info("voicemail: remote track active, waiting for pipeline", "caller", caller)
			var discarded int
			for {
				d.mu.Lock()
				pip := d.pipeline
				d.mu.Unlock()
				if pip != nil {
					slog.Info("voicemail: pipeline ready", "caller", caller, "discarded", discarded)
					break
				}
				pkt, _, err := track.ReadRTP()
				if err != nil {
					slog.Info("voicemail: remote track ended waiting for pipeline", "caller", caller, "discarded", discarded)
					return
				}
				vmPM.Decode(pkt.Payload) //nolint:errcheck
				discarded++
			}

			// Phase 2: drain to live, same heuristic as remoteTrackHandler:
			// once a ReadRTP call blocks more than 5ms, the buffered backlog
			// is exhausted and we are tracking live audio.
			drainStart := time.Now()
			var drained int
			var lastSeq uint16
			for {
				start := time.Now()
				pkt, _, err := track.ReadRTP()
				readTime := time.Since(start)
				if err != nil {
					slog.Info("voicemail: remote track ended during drain", "caller", caller)
					return
				}
				vmPM.Decode(pkt.Payload) //nolint:errcheck
				drained++
				lastSeq = pkt.SequenceNumber
				if readTime > 5*time.Millisecond {
					slog.Info("voicemail: drain complete", "caller", caller, "packets_skipped", drained-1, "duration", time.Since(drainStart).Round(time.Microsecond), "last_seq", lastSeq)
					break
				}
			}

			// Phase 3: live. Each packet is decoded for the PCM channel AND,
			// once recording is armed, the raw payload is teed into the
			// recorder. On atCap, finalize under recorderMu and trigger the
			// controller-side hangup via VoicemailRecordEnded.
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					slog.Info("voicemail: remote track ended", "caller", caller, "frames", frameCount)
					return
				}

				pcm, decErr := vmPM.Decode(pkt.Payload)
				if decErr != nil {
					slog.Warn("voicemail: decode error", "caller", caller, "error", decErr, "pkt_bytes", len(pkt.Payload))
					continue
				}

				// Tee into the recorder only once the greeting goroutine has
				// armed recording. Before that the live track carries the
				// caller listening to the greeting; appending it would prepend
				// the greeting duration of silence to the stored message.
				d.recorderMu.Lock()
				rec := d.recorder
				d.recorderMu.Unlock()
				if rec != nil && d.voicemailRecordArmed.Load() {
					atCap, err := rec.AppendFrame(pkt.Payload)
					if err != nil {
						// ErrRecorderClosed means VoicemailPickup finalized the
						// recorder between the read above and this append. That
						// is expected, not a failure, so stay quiet. Crucially
						// do NOT return: unlike the *97 greeting loop this
						// goroutine still has to decode caller audio into
						// webrtcCh, which after pickup is the live call's audio.
						if !errors.Is(err, voicemail.ErrRecorderClosed) {
							slog.Warn("voicemail: append frame failed", "caller", caller, "error", err)
						}
					} else if atCap {
						slog.Info("voicemail: max duration reached, beeping caller", "caller", caller)
						d.recorderMu.Lock()
						if d.recorder != nil {
							if _, err := d.recorder.Finalize(); err != nil {
								slog.Error("voicemail: finalize on cap failed", "caller", caller, "error", err)
							}
							d.recorder = nil
						}
						d.recorderMu.Unlock()
						// Beep the caller before hanging up so a runaway
						// recording that hits the 10-minute cap is not dropped
						// into silent dead air. The local mic is muted during
						// recording, but PlayGreetingBeep injects into the
						// outbound beep slot, so the caller hears it. The sleep
						// lets the beep drain before VoicemailRecordEnded tears
						// the call down.
						d.mu.Lock()
						pipeline := d.pipeline
						d.mu.Unlock()
						if pipeline != nil {
							pipeline.PlayGreetingBeep(500 * time.Millisecond)
							time.Sleep(600 * time.Millisecond)
						}
						go d.VoicemailRecordEnded()
						return
					}
				}

				if vmPM.InboundMuted() {
					continue
				}
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				frameCount++
				select {
				case webrtcCh <- frame:
				default:
				}
			}
		}()
	}

	// Gate ICE candidates behind answer SDP send (mirrors AnswerCall).
	sdpSent := make(chan struct{})
	d.peerMgr.OnICECandidate = func(candidate string) {
		<-sdpSent
		sendSignal(d.sig, &sigclient.Message{
			Type:      sigclient.TypeICE,
			To:        caller,
			Candidate: candidate,
		})
	}

	answerSDP, err := d.peerMgr.AcceptOffer(offerSDP)
	if err != nil {
		slog.Error("voicemail: accept offer failed", "caller", caller, "error", err)
		close(sdpSent)
		return
	}

	for _, candidate := range d.pendingICE {
		if err := d.peerMgr.AddICECandidate(candidate); err != nil {
			slog.Warn("voicemail: add queued ICE candidate failed", "caller", caller, "error", err)
		}
	}
	d.pendingICE = nil

	sendSignal(d.sig, &sigclient.Message{
		Type: sigclient.TypeAnswer,
		To:   caller,
		SDP:  answerSDP,
	})
	close(sdpSent)

	rec, err := d.voicemailStore.BeginRecording()
	if err != nil {
		slog.Error("voicemail: begin recording failed", "caller", caller, "error", err)
		d.mu.Unlock()
		d.ctrl.Reset()
		d.HangupCall()
		d.mu.Lock()
		return
	}
	d.recorderMu.Lock()
	d.recorder = rec
	d.recorderMu.Unlock()

	if d.pipeline == nil {
		d.pipeline = d.newPipeline()
		if err := d.pipeline.Start(); err != nil {
			slog.Error("voicemail: pipeline start failed", "caller", caller, "error", err)
			d.mu.Unlock()
			d.ctrl.Reset()
			d.HangupCall()
			d.mu.Lock()
			return
		}
		d.startEncodeLoop()
	}

	// Greeting + beep playback runs off the main goroutine so the daemon
	// stays responsive. The flow: 500ms lead-in silence so the caller's
	// audio path is up and decoded, then the greeting (custom .frames or
	// the default WAV), then the 500ms beep, a 500ms tail before muting
	// the local mic (so the caller's mic doesn't bleed into the outbound
	// stream), then transition to recording.
	pipeline := d.pipeline
	ctrl := d.ctrl
	go func() {
		defer recoverGoroutine("voicemail-greeting")
		time.Sleep(500 * time.Millisecond)
		d.playVoicemailGreeting(pipeline)
		pipeline.PlayGreetingBeep(500 * time.Millisecond)
		time.Sleep(500 * time.Millisecond)
		if ctrl.State() == phone.StateVOICEMAIL_GREETING {
			pipeline.SetMuted(true)
			// Arm the recorder tee now: the beep has finished, so the next
			// caller frames are the actual message. Anything the remote
			// track delivered earlier (the caller listening to the greeting)
			// was decoded but not recorded.
			d.voicemailRecordArmed.Store(true)
			ctrl.SetVoicemailRecording()
		}
	}()

	slog.Info("voicemail: auto-answered", "caller", caller, "sync_elapsed", time.Since(t0).Round(time.Microsecond))
}

// VoicemailRecordEnded is invoked when the recorder finalizes itself (max
// duration cap reached). It resets the controller to IDLE and tears down the
// call. HangupCall handles any remaining recorder cleanup defensively, so a
// late finalize during teardown is a no-op.
//
// After teardown we re-emit the LED state: a freshly finalized message bumps
// the unheard count, and the controller's HangupCall flow already issued
// LED:OFF before the file landed on disk, so without this explicit kick the
// indicator would not light up until the next idle transition.
func (d *daemonCallbacks) VoicemailRecordEnded() {
	// d.callPeer is still set here; HangupCall (called below) is what clears it.
	d.mu.Lock()
	peer := d.callPeer
	d.mu.Unlock()
	slog.Info("voicemail: recording ended", "peer", peer)

	d.ctrl.Reset()
	d.HangupCall()
	d.evaluateLED()
	// At-cap finalize already ran in the OnTrack loop before VoicemailRecordEnded
	// was scheduled, so HangupCall's finalizedVoicemail branch did not fire and
	// did not publish. Publish here so the server learns about the new message.
	d.publishVoicemailState()
}

func (d *daemonCallbacks) VoicemailEnabled() (bool, time.Duration) {
	if d.voicemailStore == nil {
		return false, 0
	}
	return d.cfg.Voicemail.Enabled, d.cfg.Voicemail.RingTimeout
}

// VoicemailRecordGreeting is invoked by the controller when the user dials
// *97 to record a custom outgoing greeting. It brings up the audio pipeline
// (without a WebRTC peer; mic capture only), plays a short prompt beep,
// opens a greeting recorder, and starts a goroutine that encodes mic frames
// to Opus and appends them. Recording ends on # (VoicemailRecordGreetingKey),
// hook-on (HangupCall path), or duration cap (atCap branch in the loop).
//
// On any error before the recording goroutine starts, the FSM is reset to
// DIALTONE with dial tone re-armed so the user lands somewhere coherent.
func (d *daemonCallbacks) VoicemailRecordGreeting() {
	d.mu.Lock()
	store := d.voicemailStore
	d.mu.Unlock()

	if store == nil {
		slog.Warn("voicemail: store not available for greeting record")
		d.ctrl.ResetToDialtone()
		d.SendTone(phone.ToneDial)
		return
	}

	d.mu.Lock()
	if d.pipeline == nil {
		d.pipeline = d.newPipeline()
		if err := d.pipeline.Start(); err != nil {
			slog.Error("voicemail: greeting pipeline start failed", "error", err)
			d.pipeline = nil
			d.mu.Unlock()
			d.ctrl.ResetToDialtone()
			d.SendTone(phone.ToneDial)
			return
		}
	}
	pipeline := d.pipeline
	d.mu.Unlock()

	rec, err := store.BeginGreetingRecording()
	if err != nil {
		slog.Error("voicemail: begin greeting recording failed", "error", err)
		d.ctrl.ResetToDialtone()
		d.SendTone(phone.ToneDial)
		return
	}

	enc, err := codec.NewEncoder(48000, 1, 24000)
	if err != nil {
		slog.Error("voicemail: greeting encoder failed", "error", err)
		rec.Discard()
		d.ctrl.ResetToDialtone()
		d.SendTone(phone.ToneDial)
		return
	}

	d.greetingRecorderMu.Lock()
	d.greetingRecorder = rec
	d.greetingEncoder = enc
	d.greetingRecorderMu.Unlock()

	// Spoken prompt ("Record your greeting after the tone") followed by the
	// beep so the user knows the recorder is hot. The *97 flow has no WebRTC
	// peer, so the beep is played through the mixer into the earpiece rather
	// than injected into the outbound capture path (which nobody would hear).
	// waitForOnceComplete gates the recording goroutine until the beep has
	// finished, keeping it out of the recording itself.
	d.playAnnouncementSequence("vm_record_greeting")
	beep := audio.SynthGreetingBeep(48000, 300*time.Millisecond)
	d.mixer.PlayOnceSamples(beep)
	waitForOnceComplete(d.mixer, 10*time.Second)
	time.Sleep(30 * time.Millisecond)

	slog.Info("voicemail: recording custom greeting")

	// Mic -> Opus -> Recorder loop. Exits when the recorder/encoder pair is
	// cleared (finalize, hangup) or the pipeline's OutFrames channel closes.
	go func() {
		defer recoverGoroutine("greeting-record")
		slog.Info("voicemail: greeting record started")
		for frame := range pipeline.OutFrames() {
			d.greetingRecorderMu.Lock()
			rec := d.greetingRecorder
			enc := d.greetingEncoder
			d.greetingRecorderMu.Unlock()
			if rec == nil || enc == nil {
				return
			}

			payload, err := enc.Encode(frame)
			if err != nil {
				slog.Warn("voicemail: greeting encode error", "error", err)
				continue
			}

			atCap, err := rec.AppendFrame(payload)
			if err != nil {
				if errors.Is(err, voicemail.ErrRecorderClosed) {
					// finalizeGreetingRecording closed the recorder out from
					// under this loop (the # key, hang-up, or duration cap).
					// The greeting is already saved; this frame just lost the
					// race. Stop quietly instead of logging a spurious WARN.
					return
				}
				slog.Warn("voicemail: greeting append failed", "error", err)
				continue
			}
			if atCap {
				slog.Info("voicemail: greeting max duration reached")
				d.finalizeGreetingRecording()
				return
			}
		}
	}()
}

// finalizeGreetingRecording closes the active greeting recorder, stops the
// audio pipeline (no live call to keep it open), resets the FSM to DIALTONE,
// and re-arms dial tone. Idempotent: safe to call from any of the three
// terminator paths (#, hook-on, max-duration). The first caller wins; the
// rest see a nil recorder and return early.
func (d *daemonCallbacks) finalizeGreetingRecording() {
	d.greetingRecorderMu.Lock()
	rec := d.greetingRecorder
	d.greetingRecorder = nil
	d.greetingEncoder = nil
	d.greetingRecorderMu.Unlock()

	if rec == nil {
		return
	}

	if _, err := rec.Finalize(); err != nil {
		slog.Error("voicemail: greeting finalize failed", "error", err)
	} else {
		slog.Info("voicemail: custom greeting saved")
		d.playAnnouncementSequence("vm_greeting_saved")
	}

	// Stop the pipeline asynchronously: pion's Stop can take a noticeable
	// fraction of a second and there's no live call holding it open.
	d.mu.Lock()
	pipeline := d.pipeline
	d.pipeline = nil
	d.mu.Unlock()
	if pipeline != nil {
		go func() {
			defer recoverGoroutine("greeting-pipeline-stop")
			pipeline.Stop()
		}()
	}

	d.ctrl.ResetToDialtone()
	d.mixer.StopTone()
	d.SendTone(phone.ToneDial)
}

// VoicemailRecordGreetingKey routes DTMF keys received while the FSM is in
// VOICEMAIL_RECORD_GREETING. Only "#" terminates the session today; other
// digits are ignored so a user typing into the recording doesn't accidentally
// kill it.
func (d *daemonCallbacks) VoicemailRecordGreetingKey(digit string) {
	if digit == "#" {
		d.finalizeGreetingRecording()
	}
}

// greetingAuditionTimeout bounds how long VoicemailPlayGreeting waits for the
// one-shot greeting playback to drain. It must exceed the voicemail package's
// 60s greeting recording cap so a full-length custom greeting always plays to
// completion; the slack covers the embedded default and mixer scheduling jitter.
const greetingAuditionTimeout = 65 * time.Second

// VoicemailPlayGreeting is invoked by the controller when the user dials *96
// to audition the active outgoing greeting. It plays a short spoken intro
// ("Your current answering machine greeting is..."), then the active greeting:
// the custom greeting if one is recorded, otherwise the embedded default. Both
// play through the mixer into the earpiece, after which the FSM returns to dial
// tone.
//
// Pure read-only: no recorder is opened and the stored greeting is never
// touched. A hook-on mid-audition routes through VoicemailExitGreetingPlayback,
// which clears the one-shot queue; FinishGreetingAudition then reports that the
// controller has left StateVOICEMAIL_PLAY_GREETING and this goroutine exits
// without re-arming dial tone, so a tone never loops against an on-hook handset.
func (d *daemonCallbacks) VoicemailPlayGreeting() {
	defer recoverGoroutine("voicemail-play-greeting")

	samples, custom := d.decodeCustomGreeting()
	if !custom {
		samples = d.mixer.ToneSamples("voicemail_greeting")
	}

	if len(samples) == 0 {
		// No custom greeting decoded and the embedded default is missing.
		// Nothing to audition; fall through to the dial-tone re-arm so the
		// user is not stranded in the audition state. The intro is skipped
		// too: announcing a greeting that will not play would be misleading.
		slog.Warn("voicemail: no greeting available to audition")
	} else {
		slog.Info("voicemail: auditioning greeting", "custom", custom, "samples", len(samples))
		// Spoken intro, then the greeting itself.
		d.playAnnouncementSequence("vm_current_greeting")
		// A hook-on during the intro already left the audition state; skip the
		// greeting one-shot rather than play it into an on-hook handset.
		if d.ctrl.State() == phone.StateVOICEMAIL_PLAY_GREETING {
			d.mixer.PlayOnceSamples(samples)
			waitForOnceComplete(d.mixer, greetingAuditionTimeout)
		}
	}

	// FinishGreetingAudition atomically checks the FSM state and, only if the
	// audition is still active, transitions to DIALTONE and re-arms dial tone.
	// A hook-on that races this is fully serialized by the controller lock, so
	// there is no window where dial tone could re-arm on an idle phone.
	if !d.ctrl.FinishGreetingAudition() {
		slog.Info("voicemail: greeting audition ended by hook-on")
	}
}

// VoicemailExitGreetingPlayback is invoked by the controller on hook-on while
// the FSM is in StateVOICEMAIL_PLAY_GREETING. It clears the one-shot queue so
// the auditioned greeting stops immediately; the VoicemailPlayGreeting
// goroutine then unblocks from waitForOnceComplete and exits without re-arming
// dial tone.
func (d *daemonCallbacks) VoicemailExitGreetingPlayback() {
	d.mixer.StopOnce()
}

// VoicemailDeleteGreeting removes the on-disk custom greeting (if any). The
// next voicemail auto-answer will fall back to the embedded default WAV.
// Idempotent on missing file (Store.DeleteGreeting handles the not-exist case).
func (d *daemonCallbacks) VoicemailDeleteGreeting() {
	d.mu.Lock()
	store := d.voicemailStore
	d.mu.Unlock()

	if store == nil {
		return
	}

	if err := store.DeleteGreeting(); err != nil {
		slog.Error("voicemail: delete greeting failed", "error", err)
	} else {
		slog.Info("voicemail: custom greeting deleted")
		// The controller already transitioned to DIALTONE and started
		// the dial tone loop before spawning this goroutine. Stop the
		// loop, play the confirmation, then restart dial tone.
		d.mixer.StopTone()
		d.playAnnouncementSequence("vm_greeting_deleted")
		d.SendTone(phone.ToneDial)
	}
}

// VoicemailRetrievalCode returns the hardcoded retrieval code that the
// controller uses to intercept dial-collection and enter VOICEMAIL_PLAYBACK.
func (d *daemonCallbacks) VoicemailRetrievalCode() string {
	return config.VoicemailRetrievalCode
}

// VoicemailEnterPlayback opens the first unheard message and streams it to
// the earpiece via the mixer's WebRTC source. Invoked by the controller
// under c.mu after the FSM transitions to VOICEMAIL_PLAYBACK. Any path
// that needs to call back into the controller (ResetToDialtone on the
// empty / refused / error paths) is deferred to a goroutine so we do not
// recurse on c.mu.
//
// Mute invariant: there must be no active 2-party peer when we start
// playback. The FSM gates retrieval through StateDIALING (off-hook, no
// peer), so callPeer should always be empty here; the assertion is
// defensive. We never play voicemail audio into a live call.
func (d *daemonCallbacks) VoicemailEnterPlayback() {
	d.mu.Lock()
	store := d.voicemailStore
	peer := d.callPeer
	d.mu.Unlock()

	if peer != "" {
		// Should never happen given the FSM gating; if it does, refuse so a
		// stale 2-party path never carries voicemail audio to the far end.
		slog.Error("voicemail: retrieval requested with active peer, refusing", "peer", peer)
		go d.voicemailExitToDialtoneAsync()
		return
	}
	if store == nil {
		slog.Warn("voicemail: store unavailable for retrieval")
		go d.voicemailExitToDialtoneAsync()
		return
	}

	var (
		sess       *voicemailPlaybackSession
		openErr    error
		hasSaved   bool
		savedCount int
	)

	d.voicemailMu.Lock()
	// Register the mixer source for the whole playback session. Per-message
	// goroutines reuse this channel; lifetime ends in VoicemailExitPlayback
	// or on the end-of-messages fallback. Re-registering with the same key
	// is idempotent (returns the existing channel).
	d.voicemailMixerCh = d.mixer.AddWebRTCSource("voicemail")
	// Fresh per-message announcement state for this session. The announce
	// flag is decided once the unheard count is known (below); the sequence
	// counter starts at 0 and openNextUnheardLocked bumps it to 1 for the
	// first message.
	d.voicemailMessageSeq = 0
	d.voicemailAnnounceNumbers = false
	// Snapshot the already-heard messages as the saved-review queue before
	// playback marks any new message heard.
	d.snapshotSavedQueueLocked(store)
	savedCount = len(d.voicemailSavedQueue)
	hasSaved = savedCount > 0
	sess, openErr = d.openNextUnheardLocked(store, 0)
	if openErr != nil || (sess == nil && !hasSaved) {
		// Error, or a truly empty mailbox: release the mixer source. When
		// there are no unheard messages but saved ones exist, the source is
		// kept so the saved-review phase can play through it.
		d.closeMixerSourceLocked()
	}
	d.voicemailMu.Unlock()

	if openErr != nil {
		slog.Error("voicemail: open first message failed", "error", openErr)
		go d.voicemailExitToDialtoneAsync()
		return
	}
	if sess == nil && !hasSaved {
		slog.Info("voicemail: no messages on entry")
		// Announce, then exit, on a goroutine. VoicemailEnterPlayback runs
		// under the controller's c.mu; blocking here for the clip duration
		// would stall every other phone event until it finished.
		go func() {
			d.playAnnouncementSequence("vm_no_messages")
			d.voicemailExitToDialtoneAsync()
		}()
		return
	}

	// Announce counts and run playback on a goroutine. VoicemailEnterPlayback
	// runs under the controller's c.mu; blocking here for the multi-clip
	// announcements would stall every other phone event (including hook-on)
	// until they finished. The loop's nil-channel and ctx-cancelled guards
	// cover a hang-up that races the announcement.
	if sess != nil {
		slog.Info("voicemail: playback start", "id", sess.id)
		go func() {
			if n, err := store.UnheardCount(); err == nil {
				d.announceMessageCount(n)
				// Two or more messages get a spoken "Message N" before each
				// one. A lone message is already identified by the count
				// intro, so it is left unannounced.
				d.voicemailMu.Lock()
				d.voicemailAnnounceNumbers = n >= 2
				d.voicemailMu.Unlock()
			}
			d.playAnnouncementSequence("vm_playback_controls")
			d.voicemailPlaybackLoop(sess)
		}()
		return
	}

	// No unheard messages, but saved ones exist: skip straight into the
	// saved-review phase rather than reporting an empty mailbox.
	slog.Info("voicemail: no unheard messages, entering saved review", "saved", savedCount)
	go d.transitionToSavedPhase(store, true)
}

// VoicemailExitPlayback tears down the current playback session and the
// mixer source. Invoked by the controller under c.mu on hook-on. Does NOT
// call into d.ctrl, so it is safe to take voicemailMu here without the
// async-defer dance.
func (d *daemonCallbacks) VoicemailExitPlayback() {
	d.voicemailMu.Lock()
	sess := d.teardownPlaybackLocked()
	d.closeMixerSourceLocked()
	// Release the saved-review snapshot for this session. snapshotSavedQueueLocked
	// reinitializes both at the next entry, so this is just tidiness: it keeps
	// the queue from lingering between sessions.
	d.voicemailSavedQueue = nil
	d.voicemailSavedCursor = -1
	d.voicemailMu.Unlock()
	if sess != nil {
		slog.Info("voicemail: playback exit", "id", sess.id)
	}
	d.evaluateLED()
}

// VoicemailKey routes DTMF received in StateVOICEMAIL_PLAYBACK. Invoked from
// a controller-spawned goroutine, so c.mu is not held when we run; we can
// safely take voicemailMu and call d.ctrl.ResetToDialtone() after
// releasing it.
//
// Digit semantics:
//
//	7: delete the current message, advance
//	9: in the new phase, mark the current message heard and advance; in
//	   saved review it just advances (the message is already heard)
//	#: skip without changing heard state
//	*: replay the current message from the start
//
// When the new-message phase runs out and saved messages exist, "7"/"9"/"#"
// cross into the saved-review phase instead of ending playback.
func (d *daemonCallbacks) VoicemailKey(digit string) {
	switch digit {
	case "7", "9", "#", "*":
	default:
		slog.Info("voicemail: ignored DTMF in playback", "digit", digit)
		return
	}

	d.mu.Lock()
	store := d.voicemailStore
	d.mu.Unlock()
	if store == nil {
		slog.Warn("voicemail: key received but store unavailable", "digit", digit)
		return
	}

	var (
		next      *voicemailPlaybackSession
		openErr   error
		noNext    bool
		mutated   bool // true when a delete/mark changed the store; triggers a (deduped) publish
		goToSaved bool // true when a new-phase key exhausted the unheard messages and saved review follows
	)

	d.voicemailMu.Lock()
	current := d.voicemailPlayback
	if current == nil {
		// Playback already ended (race with hookup or EOF). Drop the key.
		d.voicemailMu.Unlock()
		slog.Info("voicemail: key ignored, no active playback", "digit", digit)
		return
	}
	currentID := current.id
	currentNumber := current.number
	currentSaved := current.saved

	// Tear down the current message's session before mutating the store and
	// opening the next. teardownPlaybackLocked cancels the goroutine and
	// closes its player; any frames still in flight will be discarded by
	// the goroutine's select on ctx.Done.
	d.teardownPlaybackLocked()

	if currentSaved {
		// Saved-review phase. The message is already heard, so "9" has
		// nothing to mark and simply advances like "#".
		switch digit {
		case "7":
			if err := store.Delete(currentID); err != nil {
				slog.Warn("voicemail: delete failed", "id", currentID, "error", err)
			} else {
				mutated = true
			}
			next = d.openNextSavedLocked(store)
		case "9", "#":
			next = d.openNextSavedLocked(store)
		case "*":
			next, openErr = d.reopenLocked(store, currentID, 0, true)
		}
		if openErr != nil || next == nil {
			d.closeMixerSourceLocked()
			if openErr == nil {
				noNext = true
			}
		}
	} else {
		// New-message phase.
		switch digit {
		case "7":
			if err := store.Delete(currentID); err != nil {
				slog.Warn("voicemail: delete failed", "id", currentID, "error", err)
			} else {
				mutated = true
			}
			next, openErr = d.openNextUnheardLocked(store, 0)
		case "9":
			if err := store.MarkHeard(currentID); err != nil {
				slog.Warn("voicemail: mark heard failed", "id", currentID, "error", err)
			} else {
				mutated = true
			}
			next, openErr = d.openNextUnheardLocked(store, 0)
		case "#":
			// "#" leaves the heard flag untouched, so the scan must skip past
			// the current message or it would replay (currentID is still
			// unheard and would match again as the first hit).
			next, openErr = d.openNextUnheardLocked(store, currentID)
		case "*":
			next, openErr = d.reopenLocked(store, currentID, currentNumber, false)
		}
		if openErr != nil {
			d.closeMixerSourceLocked()
		} else if next == nil {
			// Unheard messages exhausted. Cross into saved review when there
			// are saved messages; the mixer source stays open for it.
			if len(d.voicemailSavedQueue) > 0 {
				goToSaved = true
			} else {
				// No follow-on session: tear down the persistent mixer source
				// so when ResetToDialtone fires the user lands cleanly on the
				// dial tone path with no voicemail PCM leaking into the mix.
				d.closeMixerSourceLocked()
				noNext = true
			}
		}
	}
	d.voicemailMu.Unlock()

	// Publish only after voicemailMu is released so the network send does
	// not hold the playback mutex. Dedup inside publishVoicemailState makes
	// this a no-op when the unheard count did not actually change.
	if mutated {
		d.publishVoicemailState()
	}

	if openErr != nil {
		slog.Error("voicemail: advance failed", "digit", digit, "error", openErr)
		d.ctrl.ResetToDialtone()
		d.SendTone(phone.ToneDial)
		d.evaluateLED()
		return
	}

	// "7" (delete) earns a spoken confirmation in either phase. "9" (save)
	// is only meaningful in the new phase; in saved review it just advances.
	var actionClip string
	switch {
	case digit == "7":
		actionClip = "vm_message_deleted"
	case digit == "9" && !currentSaved:
		actionClip = "vm_message_saved"
	}

	if goToSaved {
		slog.Info("voicemail: unheard exhausted after key, entering saved review", "last_id", currentID, "digit", digit)
		// Play the action confirmation, then transitionToSavedPhase announces
		// the saved count and plays the first saved message.
		if actionClip != "" {
			d.playAnnouncementSequence(actionClip)
		}
		d.transitionToSavedPhase(store, false)
		return
	}

	if noNext {
		slog.Info("voicemail: end of messages after key", "last_id", currentID, "digit", digit)
		// Announce action + end-of-messages, then dial tone. The exit
		// helper drains the one-shot queue before re-arming the loop so
		// the announcement and dial tone never overlap.
		if actionClip != "" {
			d.playAnnouncementSequence(actionClip, "vm_end_of_messages")
		} else {
			d.playAnnouncementSequence("vm_end_of_messages")
		}
		d.voicemailExitToDialtoneAsync()
		return
	}

	// Announce the action before starting the next message's playback.
	if actionClip != "" {
		d.playAnnouncementSequence(actionClip)
	}

	slog.Info("voicemail: advanced to next", "from", currentID, "to", next.id, "digit", digit)
	go d.voicemailPlaybackLoop(next)
}

// closeMixerSourceLocked removes the "voicemail" mixer source if one is
// registered and clears voicemailMixerCh. Idempotent: safe to call when the
// source is already torn down. Caller must hold voicemailMu.
func (d *daemonCallbacks) closeMixerSourceLocked() {
	if d.voicemailMixerCh == nil {
		return
	}
	d.mixer.RemoveWebRTCSource("voicemail")
	d.voicemailMixerCh = nil
}

// teardownPlaybackLocked cancels the current playback goroutine, closes its
// player, and clears the session pointer. Caller must hold voicemailMu.
// Returns the prior session (or nil if there was none) so the caller can
// log it or read sess.id without re-deriving.
func (d *daemonCallbacks) teardownPlaybackLocked() *voicemailPlaybackSession {
	sess := d.voicemailPlayback
	if sess == nil {
		return nil
	}
	sess.cancel()
	if err := sess.player.Close(); err != nil {
		slog.Warn("voicemail: close player on teardown", "id", sess.id, "error", err)
	}
	d.voicemailPlayback = nil
	return sess
}

// openNextUnheardLocked picks the next message whose Heard flag is false and
// whose ID is strictly greater than afterID, opens a Player for it, and
// installs a new playback session. Pass afterID=0 to start from the oldest
// unheard message; the "#" skip path passes the current message's ID so the
// scan advances past it (since "#" leaves the heard flag untouched, otherwise
// the same message would replay). Returns (nil, nil) when there are no more
// unheard messages. Caller must hold voicemailMu and have already torn down
// any prior session.
func (d *daemonCallbacks) openNextUnheardLocked(store *voicemail.Store, afterID int64) (*voicemailPlaybackSession, error) {
	msgs, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	for _, m := range msgs {
		if m.Heard {
			continue
		}
		if m.ID <= afterID {
			continue
		}
		player, err := store.OpenPlayer(m.ID)
		if err != nil {
			slog.Warn("voicemail: open player failed, skipping", "id", m.ID, "error", err)
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		d.voicemailMessageSeq++
		sess := &voicemailPlaybackSession{
			ctx:    ctx,
			cancel: cancel,
			id:     m.ID,
			number: d.voicemailMessageSeq,
			player: player,
		}
		d.voicemailPlayback = sess
		return sess, nil
	}
	return nil, nil
}

// snapshotSavedQueueLocked records the IDs of every already-heard message as
// the saved-review play order for this session, ascending so oldest first.
// It must run at session entry, before playback marks any new message heard.
// Caller must hold voicemailMu.
func (d *daemonCallbacks) snapshotSavedQueueLocked(store *voicemail.Store) {
	d.voicemailSavedQueue = nil
	d.voicemailSavedCursor = -1
	msgs, err := store.List()
	if err != nil {
		slog.Warn("voicemail: list for saved snapshot failed", "error", err)
		return
	}
	for _, m := range msgs {
		if m.Heard {
			d.voicemailSavedQueue = append(d.voicemailSavedQueue, m.ID)
		}
	}
}

// openNextSavedLocked advances the saved-review cursor and opens the next
// saved message, skipping any that have been deleted since the snapshot.
// Returns nil when the saved queue is exhausted. Caller must hold voicemailMu
// and have torn down any prior session.
func (d *daemonCallbacks) openNextSavedLocked(store *voicemail.Store) *voicemailPlaybackSession {
	for {
		d.voicemailSavedCursor++
		if d.voicemailSavedCursor >= len(d.voicemailSavedQueue) {
			return nil
		}
		id := d.voicemailSavedQueue[d.voicemailSavedCursor]
		player, err := store.OpenPlayer(id)
		if err != nil {
			slog.Warn("voicemail: open saved player failed, skipping", "id", id, "error", err)
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		sess := &voicemailPlaybackSession{
			ctx:    ctx,
			cancel: cancel,
			id:     id,
			saved:  true,
			player: player,
		}
		d.voicemailPlayback = sess
		return sess
	}
}

// reopenLocked opens a fresh Player for the given message ID and installs a
// new session. Used by the "*" replay path. The replayed message keeps its
// original session position, so number is carried over from the prior
// session rather than bumping voicemailMessageSeq. saved marks whether this
// is a saved-review session. Caller must hold voicemailMu and have torn down
// any prior session.
func (d *daemonCallbacks) reopenLocked(store *voicemail.Store, id int64, number int, saved bool) (*voicemailPlaybackSession, error) {
	player, err := store.OpenPlayer(id)
	if err != nil {
		return nil, fmt.Errorf("reopen message %d: %w", id, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sess := &voicemailPlaybackSession{
		ctx:    ctx,
		cancel: cancel,
		id:     id,
		number: number,
		saved:  saved,
		player: player,
	}
	d.voicemailPlayback = sess
	return sess, nil
}

// transitionToSavedPhase announces the saved-message count and begins playing
// the saved-review queue. It runs outside voicemailMu because it plays
// blocking announcement clips. It bridges the new-message phase into saved
// review, and is also the direct entry point when *98 starts with no unheard
// messages. playControls plays the spoken-controls prompt first, used only on
// the direct-entry path where the controls have not been played yet.
func (d *daemonCallbacks) transitionToSavedPhase(store *voicemail.Store, playControls bool) {
	defer recoverGoroutine("voicemail-saved-transition")

	d.voicemailMu.Lock()
	n := len(d.voicemailSavedQueue)
	d.voicemailMu.Unlock()
	d.announceSavedCount(n)
	if playControls {
		d.playAnnouncementSequence("vm_playback_controls")
	}

	d.voicemailMu.Lock()
	sess := d.openNextSavedLocked(store)
	if sess == nil {
		d.closeMixerSourceLocked()
	}
	d.voicemailMu.Unlock()

	if sess == nil {
		// Every saved message was deleted between the snapshot and now.
		slog.Info("voicemail: saved review empty")
		d.playAnnouncementSequence("vm_end_of_messages")
		d.voicemailExitToDialtoneAsync()
		return
	}
	slog.Info("voicemail: saved review start", "id", sess.id)
	d.voicemailPlaybackLoop(sess)
}

// voicemailPlaybackLoop decodes Opus frames from the session's Player and
// pushes 48kHz mono PCM into the mixer's voicemail source channel. Exits
// on context cancel (no-op, caller already tore down) or on EOF / read
// error (transitions to the next message via voicemailAdvanceFromEOF).
func (d *daemonCallbacks) voicemailPlaybackLoop(sess *voicemailPlaybackSession) {
	defer recoverGoroutine("voicemail-playback")

	d.voicemailMu.Lock()
	ch := d.voicemailMixerCh
	d.voicemailMu.Unlock()
	if ch == nil {
		// Defensive: should never happen because VoicemailEnterPlayback
		// installs the mixer channel before opening the first session and
		// the goroutine is spawned with that channel still in place. If a
		// concurrent teardown removed it before this goroutine ran, fall
		// back to a clean exit instead of leaving the session pointer set
		// and the user stuck in StateVOICEMAIL_PLAYBACK.
		slog.Warn("voicemail: playback loop started without mixer channel", "id", sess.id)
		d.voicemailMu.Lock()
		if d.voicemailPlayback == sess {
			d.teardownPlaybackLocked()
		}
		d.closeMixerSourceLocked()
		d.voicemailMu.Unlock()
		go d.voicemailExitToDialtoneAsync()
		return
	}

	// Announce "Message N" before this message's audio when the new-message
	// phase holds two or more messages. Saved-review messages are not
	// numbered; the saved-count announcement already marked the crossover.
	// announceMessageNumber blocks for the clip duration; skip it if the
	// session was already torn down (hook-on racing the goroutine spawn).
	d.voicemailMu.Lock()
	announce := d.voicemailAnnounceNumbers && !sess.saved
	number := sess.number
	d.voicemailMu.Unlock()
	if announce {
		select {
		case <-sess.ctx.Done():
			return
		default:
			d.announceMessageNumber(number)
		}
	}

	dec, err := codec.NewDecoder(48000, 1)
	if err != nil {
		slog.Error("voicemail: decoder init failed", "id", sess.id, "error", err)
		d.voicemailAdvanceFromEOF(sess)
		return
	}

	for {
		select {
		case <-sess.ctx.Done():
			return
		default:
		}

		payload, err := sess.player.NextFrame()
		if errors.Is(err, io.EOF) {
			d.voicemailAdvanceFromEOF(sess)
			return
		}
		if err != nil {
			slog.Warn("voicemail: read frame failed", "id", sess.id, "error", err)
			d.voicemailAdvanceFromEOF(sess)
			return
		}
		pcm, err := dec.Decode(payload)
		if err != nil {
			slog.Warn("voicemail: decode failed", "id", sess.id, "error", err)
			continue
		}
		// Decode returns a slice of the decoder's internal buffer (valid
		// only until the next Decode call). Copy before queuing so the
		// mixer's render loop sees a stable backing array.
		frame := make([]int16, len(pcm))
		copy(frame, pcm)
		select {
		case ch <- frame:
		case <-sess.ctx.Done():
			return
		}
	}
}

// voicemailAdvanceFromEOF runs when a playback goroutine reaches EOF (or a
// non-fatal read error). It marks the message heard, opens the next
// unheard, and either starts the next goroutine or transitions out of
// playback. Stale: if voicemailPlayback no longer points at sess (a
// concurrent DTMF callback already moved on), do nothing.
func (d *daemonCallbacks) voicemailAdvanceFromEOF(sess *voicemailPlaybackSession) {
	d.mu.Lock()
	store := d.voicemailStore
	d.mu.Unlock()
	if store == nil {
		return
	}

	var (
		next      *voicemailPlaybackSession
		openErr   error
		noNext    bool
		isStale   bool
		mutated   bool // true when MarkHeard succeeded and the unheard count moved
		goToSaved bool // true when the new phase is done and saved review follows
	)

	d.voicemailMu.Lock()
	if d.voicemailPlayback != sess {
		// Another callback already swapped us out (e.g. DTMF dispatch
		// raced ahead of EOF). Whoever swapped has already torn down our
		// session; we don't touch state.
		isStale = true
	} else if sess.saved {
		// A saved-review message finished: advance the saved queue.
		d.teardownPlaybackLocked()
		next = d.openNextSavedLocked(store)
		if next == nil {
			d.closeMixerSourceLocked()
			noNext = true
		}
	} else {
		// A new-message played to the end: mark it heard, then find the next
		// unheard one.
		d.teardownPlaybackLocked()
		if err := store.MarkHeard(sess.id); err != nil {
			slog.Warn("voicemail: mark heard on EOF failed", "id", sess.id, "error", err)
		} else {
			mutated = true
		}
		next, openErr = d.openNextUnheardLocked(store, 0)
		if openErr != nil {
			d.closeMixerSourceLocked()
		} else if next == nil {
			// Unheard messages exhausted. Cross into the saved-review phase
			// when there are saved messages, otherwise end. The mixer source
			// is left open for the saved phase to reuse.
			if len(d.voicemailSavedQueue) > 0 {
				goToSaved = true
			} else {
				d.closeMixerSourceLocked()
				noNext = true
			}
		}
	}
	d.voicemailMu.Unlock()

	if isStale {
		return
	}
	// Publish after voicemailMu release; dedup makes redundant calls cheap.
	if mutated {
		d.publishVoicemailState()
	}
	if openErr != nil {
		slog.Error("voicemail: open next after EOF failed", "from_id", sess.id, "error", openErr)
		d.ctrl.ResetToDialtone()
		d.SendTone(phone.ToneDial)
		d.evaluateLED()
		return
	}
	if goToSaved {
		slog.Info("voicemail: unheard exhausted, entering saved review", "last_id", sess.id)
		d.transitionToSavedPhase(store, false)
		return
	}
	if noNext {
		slog.Info("voicemail: end of messages after EOF", "last_id", sess.id)
		d.playAnnouncementSequence("vm_end_of_messages")
		d.voicemailExitToDialtoneAsync()
		return
	}

	slog.Info("voicemail: advancing after EOF", "from", sess.id, "to", next.id)
	go d.voicemailPlaybackLoop(next)
}

// voicemailExitToDialtoneAsync resets the FSM to DIALTONE and re-arms the
// dial tone after any queued one-shot tone (e.g. a spoken end-of-messages
// announcement) has finished playing. The drain is required because
// PlayOnce mixes one-shot audio over any active loop, so starting the
// dial-tone loop while a clip is still in the queue would play them
// simultaneously. waitForOnceComplete bounds the wait so a stalled mixer
// cannot wedge the goroutine forever.
//
// Spawned with `go` by callers that hold c.mu (VoicemailEnterPlayback) so
// the inner ResetToDialtone does not recurse on the controller mutex.
// Callers already running in a controller-spawned goroutine (VoicemailKey,
// the playback EOF path) invoke it synchronously.
func (d *daemonCallbacks) voicemailExitToDialtoneAsync() {
	defer recoverGoroutine("voicemail-exit-to-dialtone")
	waitForOnceComplete(d.mixer, 10*time.Second)
	d.ctrl.ResetToDialtone()
	d.SendTone(phone.ToneDial)
	d.evaluateLED()
}

// evaluateLED re-emits the LED state appropriate for an idle phone with
// the current unheard-message count. Used by background mutations where
// the controller will not issue a fresh LED:OFF on its own: a recording
// that finalizes while idle, or a delete during playback that drops the
// count to zero just before the playback exit path runs.
//
// We deliberately use the same OFF -> SLOWER_PULSE rewrite the SendLED
// wrapper performs, so the two paths cannot disagree about what idle
// looks like. The wrapper is the source of truth.
func (d *daemonCallbacks) evaluateLED() {
	d.serial.LED(d.ledModeWithVoicemailHint("OFF"))
}

// playVoicemailGreeting blocks the caller goroutine until the outgoing
// greeting has finished playing. Tries the user's recorded greeting first;
// falls back to the embedded default WAV on os.ErrNotExist (no custom
// recorded) or any decode error.
func (d *daemonCallbacks) playVoicemailGreeting(pipeline *audio.Pipeline) {
	if d.playCustomGreeting(pipeline) {
		return
	}
	d.playDefaultGreeting(pipeline)
}

// playDefaultGreeting injects the embedded "voicemail_greeting" WAV samples
// into the pipeline's beep slot and sleeps for the playback duration plus a
// small tail. If the tone failed to load (e.g. asset missing on disk), logs
// a warning and returns immediately so the auto-answer path still proceeds
// to the beep + recording.
func (d *daemonCallbacks) playDefaultGreeting(pipeline *audio.Pipeline) {
	samples := d.mixer.ToneSamples("voicemail_greeting")
	if samples == nil {
		slog.Warn("voicemail: default greeting tone not loaded, skipping")
		return
	}
	pipeline.PlayGreetingSamples(samples)
	// Pipeline drains the buffer at 48kHz regardless of the WAV's authored
	// sample rate. Sleep matches that drain so the subsequent beep doesn't
	// step on the greeting tail.
	time.Sleep(greetingPlaybackDuration(len(samples)))
}

// playCustomGreeting opens the user's recorded greeting, decodes every Opus
// frame into a flat PCM buffer, injects it into the pipeline's beep slot, and
// sleeps for the playback duration. Returns true when a custom greeting played
// to completion; false on no-greeting (caller should fall back to default),
// decoder init failure, or empty buffer.
func (d *daemonCallbacks) playCustomGreeting(pipeline *audio.Pipeline) bool {
	samples, ok := d.decodeCustomGreeting()
	if !ok {
		return false
	}
	pipeline.PlayGreetingSamples(samples)
	time.Sleep(greetingPlaybackDuration(len(samples)))
	return true
}

// decodeCustomGreeting opens the user's recorded greeting and decodes every
// Opus frame into a flat 48kHz mono PCM buffer. Returns (samples, true) when a
// non-empty custom greeting decoded successfully; (nil, false) on no-greeting
// (caller should fall back to the embedded default), store/decoder init
// failure, or an empty buffer.
func (d *daemonCallbacks) decodeCustomGreeting() ([]int16, bool) {
	d.mu.Lock()
	store := d.voicemailStore
	d.mu.Unlock()
	if store == nil {
		return nil, false
	}

	player, err := store.OpenGreeting()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("voicemail: failed to open custom greeting, falling back to default", "error", err)
		}
		return nil, false
	}
	defer player.Close() //nolint:errcheck

	dec, err := codec.NewDecoder(48000, 1)
	if err != nil {
		slog.Error("voicemail: decoder for greeting failed", "error", err)
		return nil, false
	}

	// Decode every frame into a single PCM buffer. append copies pcm into
	// allSamples' backing array, so the decoder's internal buffer (valid
	// only until the next Decode call) is consumed safely each iteration.
	var allSamples []int16
	for {
		frame, err := player.NextFrame()
		if err != nil {
			break
		}
		pcm, err := dec.Decode(frame)
		if err != nil {
			slog.Warn("voicemail: greeting decode error", "error", err)
			continue
		}
		allSamples = append(allSamples, pcm...)
	}

	if len(allSamples) == 0 {
		slog.Warn("voicemail: custom greeting is empty, falling back to default")
		return nil, false
	}

	return allSamples, true
}

// greetingPlaybackDuration returns how long the given PCM sample count will
// take to drain through the 48 kHz pipeline, plus a 100ms tail so the next
// stage (beep) does not step on the greeting end.
func greetingPlaybackDuration(samples int) time.Duration {
	const (
		sampleRate = 48000
		tail       = 100 * time.Millisecond
	)
	return time.Duration(samples)*time.Second/time.Duration(sampleRate) + tail
}
