package main

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	"github.com/justinlindh/digits/pi/digitsd/internal/config"
	"github.com/justinlindh/digits/pi/digitsd/internal/contacts"
	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
)

// fakeController records the dispatch's controller-facing calls so routing
// tests can assert which handler path a message type reached. It satisfies
// signalController. State is configurable per test; everything else just
// records. All access is guarded so goroutines a handler may spawn (e.g. the
// TypeBusy retry) can poke State without racing the test's assertions.
type fakeController struct {
	mu             sync.Mutex
	state          phone.State
	signals        []signalCall
	contactChecker phone.ContactChecker
	contactSetN    int
	callReturnNum  string
	callReturnRing string
	resetCount     int
	confMember     []confMemberCall
	confConnect    []confConnectCall
	confLeave      []confLeaveCall
	confEnd        []confEndCall
	confRejected   []confRejectedCall
}

type signalCall struct{ msgType, sender string }
type confMemberCall struct {
	confID  string
	members []sigclient.ConferenceMemberInfo
}
type confConnectCall struct {
	confID, peer string
	initiator    bool
}
type confLeaveCall struct{ confID, peer, reason string }
type confEndCall struct{ confID, reason string }
type confRejectedCall struct{ confID, reason string }

func (f *fakeController) HandleSignal(msgType, sender string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signals = append(f.signals, signalCall{msgType, sender})
}

func (f *fakeController) State() phone.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeController) SetContactChecker(cc phone.ContactChecker) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contactChecker = cc
	f.contactSetN++
}

func (f *fakeController) SetCallReturnNumber(number string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callReturnNum = number
}

func (f *fakeController) ResetToDialtone() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resetCount++
}

func (f *fakeController) HandleCallReturnRing(target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callReturnRing = target
}

func (f *fakeController) HandleConferenceMember(confID string, members []sigclient.ConferenceMemberInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confMember = append(f.confMember, confMemberCall{confID, members})
}

func (f *fakeController) HandleConferenceConnect(confID, peer string, initiator bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confConnect = append(f.confConnect, confConnectCall{confID, peer, initiator})
}

func (f *fakeController) HandleConferenceLeave(confID, peer, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confLeave = append(f.confLeave, confLeaveCall{confID, peer, reason})
}

func (f *fakeController) HandleConferenceEnd(confID, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confEnd = append(f.confEnd, confEndCall{confID, reason})
}

func (f *fakeController) HandleConferenceRejected(confID, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confRejected = append(f.confRejected, confRejectedCall{confID, reason})
}

func (f *fakeController) lastSignal() (signalCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.signals) == 0 {
		return signalCall{}, false
	}
	return f.signals[len(f.signals)-1], true
}

// newDispatchDaemon builds a daemon wired for routing tests: a real mixer
// over a no-op writer (so PlayOnce/StopTone never touch ALSA), a recording
// fake controller, and the dispatch dependency fields populated with inert
// values. serial and sig are left nil; tests only feed message types whose
// synchronous path avoids them.
func newDispatchDaemon(t *testing.T, fc *fakeController) *daemonCallbacks {
	t.Helper()
	d := &daemonCallbacks{}
	d.mixer = audio.NewMixer(nopWriter{})
	d.ctrlSignal = fc
	d.contactsCache = contacts.NewCache(filepath.Join(t.TempDir(), "contacts.json"))
	return d
}

// TestDispatchRouting_FSMDelegating verifies that each message type whose
// handler delegates to the phone controller reaches the right controller
// method with the right arguments. This is the routing backbone: a dropped
// or mis-wired case here would silently break call signaling.
func TestDispatchRouting_FSMDelegating(t *testing.T) {
	t.Run("ring", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeRing, From: "3140001"})
		got, ok := fc.lastSignal()
		if !ok || got.msgType != "ring" {
			t.Fatalf("ring routed to %+v, want HandleSignal(ring)", got)
		}
		d.mu.Lock()
		caller := d.pendingCaller
		d.mu.Unlock()
		if caller != "3140001" {
			t.Errorf("pendingCaller = %q, want 3140001", caller)
		}
	})

	t.Run("answer", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeAnswer, From: "3140002"})
		got, _ := fc.lastSignal()
		if got != (signalCall{"answer", "3140002"}) {
			t.Fatalf("answer routed to %+v, want HandleSignal(answer, 3140002)", got)
		}
	})

	t.Run("hangup", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeHangup, From: "3140003"})
		got, _ := fc.lastSignal()
		if got != (signalCall{"hangup", "3140003"}) {
			t.Fatalf("hangup routed to %+v, want HandleSignal(hangup, 3140003)", got)
		}
	})

	t.Run("busy without call-return origin", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeBusy, From: "3140004"})
		got, _ := fc.lastSignal()
		if got != (signalCall{"busy", "3140004"}) {
			t.Fatalf("busy routed to %+v, want HandleSignal(busy, 3140004)", got)
		}
	})

	t.Run("conference member", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		members := []sigclient.ConferenceMemberInfo{{Phone: "3140005"}}
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeConferenceMember, ConfID: "c1", Members: members})
		if len(fc.confMember) != 1 || fc.confMember[0].confID != "c1" {
			t.Fatalf("conference member routed to %+v, want HandleConferenceMember(c1)", fc.confMember)
		}
	})

	t.Run("conference connect", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeConferenceConnect, ConfID: "c2", Peer: "3140006", Initiator: true})
		if len(fc.confConnect) != 1 || fc.confConnect[0] != (confConnectCall{"c2", "3140006", true}) {
			t.Fatalf("conference connect routed to %+v", fc.confConnect)
		}
	})

	t.Run("conference leave", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeConferenceLeave, ConfID: "c3", Peer: "3140007", Reason: "bye"})
		if len(fc.confLeave) != 1 || fc.confLeave[0] != (confLeaveCall{"c3", "3140007", "bye"}) {
			t.Fatalf("conference leave routed to %+v", fc.confLeave)
		}
	})

	t.Run("conference end", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeConferenceEnd, ConfID: "c4", Reason: "done"})
		if len(fc.confEnd) != 1 || fc.confEnd[0] != (confEndCall{"c4", "done"}) {
			t.Fatalf("conference end routed to %+v", fc.confEnd)
		}
	})

	t.Run("conference rejected", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeConferenceRejected, ConfID: "c5", Reason: "full"})
		if len(fc.confRejected) != 1 || fc.confRejected[0] != (confRejectedCall{"c5", "full"}) {
			t.Fatalf("conference rejected routed to %+v", fc.confRejected)
		}
	})

	t.Run("call return ring", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeCallReturnRing, Number: "3140008"})
		if fc.callReturnRing != "3140008" {
			t.Fatalf("call return ring target = %q, want 3140008", fc.callReturnRing)
		}
	})

	t.Run("call return result announces caller", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeCallReturnResult, Number: "3140009"})
		fc.mu.Lock()
		num := fc.callReturnNum
		fc.mu.Unlock()
		if num != "3140009" {
			t.Fatalf("call return result set number %q, want 3140009", num)
		}
	})

	t.Run("error in ADD_CALLING routes through controller", func(t *testing.T) {
		fc := &fakeController{state: phone.StateADD_CALLING}
		d := newDispatchDaemon(t, fc)
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeError, From: "3140010", Error: "boom"})
		got, ok := fc.lastSignal()
		if !ok || got != (signalCall{"error", "3140010"}) {
			t.Fatalf("error routed to %+v, want HandleSignal(error, 3140010)", got)
		}
	})
}

// TestDispatchRouting_ContactsUpdatesCacheAndChecker checks both the cache
// write and the controller wiring for the contacts path, including the
// distinction between a populated update (install checker) and an empty one
// (clear checker).
func TestDispatchRouting_ContactsUpdatesCacheAndChecker(t *testing.T) {
	for _, mt := range []string{sigclient.TypeContacts, sigclient.TypeContactsUpdated} {
		t.Run(mt+" populated", func(t *testing.T) {
			fc := &fakeController{}
			d := newDispatchDaemon(t, fc)
			d.handleSignal(&sigclient.Message{
				Type:     mt,
				Contacts: []sigclient.ContactEntry{{Number: "3140001", Name: "Alice"}},
			})
			if d.contactsCache.Count() != 1 {
				t.Errorf("cache count = %d, want 1", d.contactsCache.Count())
			}
			if !d.contactsCache.IsContact("3140001") {
				t.Errorf("expected 3140001 to be a contact")
			}
			if fc.contactChecker == nil {
				t.Errorf("expected contact checker to be installed for populated update")
			}
		})
	}

	t.Run("empty update clears checker", func(t *testing.T) {
		fc := &fakeController{}
		d := newDispatchDaemon(t, fc)
		// Seed a checker first, then clear it with an empty update.
		d.handleSignal(&sigclient.Message{
			Type:     sigclient.TypeContacts,
			Contacts: []sigclient.ContactEntry{{Number: "3140001"}},
		})
		d.handleSignal(&sigclient.Message{Type: sigclient.TypeContactsUpdated})
		if d.contactsCache.Count() != 0 {
			t.Errorf("cache count = %d after empty update, want 0", d.contactsCache.Count())
		}
		if fc.contactChecker != nil {
			t.Errorf("expected contact checker cleared for empty update")
		}
	})
}

// TestDispatchRouting_ICEServersCached confirms the ICE-servers handler
// stashes the server list on the daemon for the next peer connection.
func TestDispatchRouting_ICEServersCached(t *testing.T) {
	fc := &fakeController{}
	d := newDispatchDaemon(t, fc)
	d.handleSignal(&sigclient.Message{
		Type: sigclient.TypeICEServers,
		Servers: []sigclient.ICEServer{
			{URLs: []string{"stun:stun.example:3478"}},
			{URLs: []string{"turn:turn.example:3478"}, Username: "u", Credential: "p"},
		},
	})
	d.mu.Lock()
	n := len(d.iceServers)
	cred := ""
	if n == 2 {
		cred = d.iceServers[1].Credential
	}
	d.mu.Unlock()
	if n != 2 {
		t.Fatalf("cached %d ICE servers, want 2", n)
	}
	if cred != "p" {
		t.Errorf("second server credential = %q, want p", cred)
	}
}

// TestDispatchRouting_LineSettingsAppliesVoiceStyle exercises a pure-logic
// case end to end: a line_settings push with a new voice style mutates the
// config and persists it, while applySilentModeLive degrades to a no-op
// against the nil real controller.
func TestDispatchRouting_LineSettingsAppliesVoiceStyle(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.VoiceStyle = config.VoiceStyleCopper

	fc := &fakeController{}
	d := newDispatchDaemon(t, fc)
	d.cfg = cfg

	d.handleSignal(&sigclient.Message{
		Type:         sigclient.TypeLineSettings,
		LineSettings: &sigclient.LineSettings{VoiceStyle: "modern"},
	})

	d.mu.Lock()
	got := d.cfg.VoiceStyle
	d.mu.Unlock()
	if got != "modern" {
		t.Fatalf("voice style = %q, want modern", got)
	}

	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.VoiceStyle != "modern" {
		t.Errorf("persisted voice style = %q, want modern", reloaded.VoiceStyle)
	}
}

// TestDispatchRouting_LineSettingsNilPayloadIsNoop confirms a malformed
// line_settings message (missing payload) is dropped without mutating config.
func TestDispatchRouting_LineSettingsNilPayloadIsNoop(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.VoiceStyle = config.VoiceStyleCopper

	fc := &fakeController{}
	d := newDispatchDaemon(t, fc)
	d.cfg = cfg

	d.handleSignal(&sigclient.Message{Type: sigclient.TypeLineSettings})

	d.mu.Lock()
	got := d.cfg.VoiceStyle
	d.mu.Unlock()
	if got != config.VoiceStyleCopper {
		t.Errorf("voice style mutated on nil payload: %q", got)
	}
}

// TestDispatchRouting_UnknownTypeIsInertDefault confirms an unrecognized
// message type hits the default arm: no controller method fires and no panic.
func TestDispatchRouting_UnknownTypeIsInertDefault(t *testing.T) {
	fc := &fakeController{}
	d := newDispatchDaemon(t, fc)
	d.handleSignal(&sigclient.Message{Type: "not_a_real_type"})
	if _, ok := fc.lastSignal(); ok {
		t.Errorf("unknown type reached a controller handler: %+v", fc.signals)
	}
	if fc.contactSetN != 0 || len(fc.confMember) != 0 {
		t.Errorf("unknown type produced side effects on the controller")
	}
}

// TestDispatchRouting_DTMFGatedByState verifies the DTMF guard: a digit while
// not connected is dropped (no panic, controller untouched). The connected
// happy path is left to integration since it plays through the mixer.
func TestDispatchRouting_DTMFGatedByState(t *testing.T) {
	fc := &fakeController{state: phone.StateIDLE}
	d := newDispatchDaemon(t, fc)
	d.handleSignal(&sigclient.Message{Type: sigclient.TypeDTMF, From: "3140001", Digit: "5"})
	if _, ok := fc.lastSignal(); ok {
		t.Errorf("DTMF while idle should not reach the controller")
	}
}
