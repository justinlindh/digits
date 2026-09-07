package signaling

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestReviewLegacyEnvelopeCannotCrossLineOwnership(t *testing.T) {
	h := NewHub()
	h.SetLineResolver(func(string) (int64, error) { return 2, nil }, func(int64) (string, error) { return "3140002", nil })
	h.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	c := &Conn{LineID: 1, HardwareID: "review-stale", Send: make(chan []byte, 1)}
	if err := h.Register("3140001", c); err != nil {
		t.Fatal(err)
	}
	defer h.Unregister("3140001", c)
	h.deliverFromRedis(&Envelope{TargetType: "number", Target: "3140001", Message: &Message{Type: TypeCall}})
	select {
	case <-c.Send:
		t.Fatal("legacy envelope delivered new owner's traffic to stale line identity")
	default:
	}
}

func TestReviewFullQueueCannotStrandRenumberedSocket(t *testing.T) {
	h := NewHub()
	h.SetLineResolver(func(string) (int64, error) { return 1, nil }, func(int64) (string, error) { return "3140002", nil })
	c := &Conn{LineID: 1, HardwareID: "review-full", Send: make(chan []byte, 1)}
	if err := h.Register("3140001", c); err != nil {
		t.Fatal(err)
	}
	defer h.Unregister("3140001", c)
	h.SetVoicemailUnheard("3140001", c.HardwareID, 3)
	c.Send <- []byte("already queued")
	h.RenumberLine(1)
	if h.ConnIsCurrent(c) {
		t.Fatal("renumber control dropped on full queue without retiring stale socket")
	}
	if got := h.LineVoicemailUnheard("3140001"); got != 0 {
		t.Fatalf("retired socket left voicemail count %d", got)
	}
}

func TestReviewStaleHeartbeatCannotRecreatePresence(t *testing.T) {
	s, _ := newTestDeviceState(t)
	ctx := context.Background()
	s.SetOnline(ctx, "3140001", DevicePresence{PodID: s.PodID(), HardwareID: "review-heartbeat", ConnectionID: "old"})
	s.SetOnline(ctx, "3140002", DevicePresence{PodID: s.PodID(), HardwareID: "review-heartbeat", ConnectionID: "new"})
	s.SetOffline(ctx, "3140002", "review-heartbeat", "new")
	s.TouchLastSeen(ctx, "3140001", "review-heartbeat", "old")
	if s.IsHardwareOnline(ctx, "review-heartbeat") {
		t.Fatal("stale heartbeat recreated offline device presence")
	}
}

func TestReviewStaleDeviceInfoCannotMutateReplacementPresence(t *testing.T) {
	s, client := newTestDeviceState(t)
	ctx := context.Background()
	s.SetOnline(ctx, "3140002", DevicePresence{PodID: s.PodID(), HardwareID: "review-info", ConnectionID: "new", PiVersion: "new"})
	s.UpdateDeviceInfo(ctx, "review-info", "old", DevicePresence{PiVersion: "stale"})
	got := client.HGet(deviceKeyPrefix+"review-info", "pi_version")
	if got != "new" {
		t.Fatalf("stale device info changed replacement presence to %q", got)
	}
}

func TestReviewBindingValidationRetiresMissedRenumber(t *testing.T) {
	h := NewHub()
	h.SetLineResolver(nil, func(int64) (string, error) { return "3140002", nil })
	c := &Conn{LineID: 1, HardwareID: "review-missed", Send: make(chan []byte, 1)}
	if err := h.Register("3140001", c); err != nil {
		t.Fatal(err)
	}
	if h.EnsureConnBindingCurrent(c) {
		t.Fatal("stale binding passed validation")
	}
	if h.ConnIsCurrent(c) {
		t.Fatal("binding validation did not retire stale socket")
	}
}

func TestReviewPresenceForOldOwnerDoesNotMakeReusedNumberOnline(t *testing.T) {
	ds, _ := newTestDeviceState(t)
	ctx := context.Background()
	ds.SetOnline(ctx, "3140099", DevicePresence{PodID: ds.PodID(), HardwareID: "stale-owner", ConnectionID: "old", LineID: 1})
	h := NewHub()
	h.SetDeviceState(ds)
	h.SetLineResolver(func(string) (int64, error) { return 2, nil }, nil)
	if h.IsOnline("3140099") {
		t.Fatal("stale old-owner presence made reused number online")
	}
}

func TestReviewLegacyPresenceAvailableOnlyWhileRenumberFrozen(t *testing.T) {
	ds, _ := newTestDeviceState(t)
	ctx := context.Background()
	if err := ds.client.HSet(ctx, deviceKeyPrefix+"legacy-owner", "number", "3140088", "hardware_id", "legacy-owner").Err(); err != nil {
		t.Fatal(err)
	}
	if err := ds.client.SAdd(ctx, lineDevicesPrefix+"3140088", "legacy-owner").Err(); err != nil {
		t.Fatal(err)
	}
	h := NewHub()
	h.SetDeviceState(ds)
	h.SetLineResolver(func(string) (int64, error) { return 8, nil }, nil)
	legacyAllowed := true
	h.SetLegacyIdentityAllowedResolver(func() (bool, error) { return legacyAllowed, nil })
	if !h.IsOnline("3140088") {
		t.Fatal("legacy presence unavailable during default-off rolling deployment")
	}
	legacyAllowed = false
	if h.IsOnline("3140088") {
		t.Fatal("legacy presence remained authoritative after renumber activation")
	}
}

func TestReviewLegacyEnvelopeRejectedAfterNumberReuse(t *testing.T) {
	h := NewHub()
	h.SetLineResolver(func(string) (int64, error) { return 2, nil }, nil)
	h.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	newOwner := &Conn{LineID: 2, HardwareID: "new-owner", Send: make(chan []byte, 1)}
	if err := h.Register("3140001", newOwner); err != nil {
		t.Fatal(err)
	}
	h.deliverFromRedis(&Envelope{TargetType: "number", Target: "3140001", Message: &Message{Type: TypeCall}})
	select {
	case <-newOwner.Send:
		t.Fatal("delayed legacy envelope reached new owner after number reuse")
	default:
	}
}

func TestReviewLegacyEnvelopeAvailableWhileRenumberFrozen(t *testing.T) {
	h := NewHub()
	h.SetLineResolver(func(string) (int64, error) { return 2, nil }, nil)
	h.SetLegacyIdentityAllowedResolver(func() (bool, error) { return true, nil })
	owner := &Conn{LineID: 2, HardwareID: "current-owner", Send: make(chan []byte, 1)}
	if err := h.Register("3140001", owner); err != nil {
		t.Fatal(err)
	}
	h.deliverFromRedis(&Envelope{TargetType: "number", Target: "3140001", Message: &Message{Type: TypeCall}})
	select {
	case <-owner.Send:
	default:
		t.Fatal("legacy envelope unavailable during frozen rolling deployment")
	}
}

func TestReviewNumberScopedGettersExcludeStaleOwner(t *testing.T) {
	h := NewHub()
	h.SetLineResolver(func(string) (int64, error) { return 2, nil }, nil)
	h.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	stale := &Conn{LineID: 1, HardwareID: "stale-getter", PiVersion: "secret", Send: make(chan []byte, 1)}
	if err := h.Register("3140077", stale); err != nil {
		t.Fatal(err)
	}
	if h.Get("3140077") != nil || len(h.GetAll("3140077")) != 0 || h.ConnectionCount("3140077") != 0 {
		t.Fatal("number-scoped connection getter exposed stale owner")
	}
	if got := h.AllDeviceInfo("3140077"); len(got) != 0 {
		t.Fatalf("number-scoped device info exposed stale owner: %+v", got)
	}
}

func TestLegacyIdentityRemainsRejectedAfterWriteGateRefreeze(t *testing.T) {
	h := NewHub()
	h.SetLineResolver(func(string) (int64, error) { return 8, nil }, nil)
	h.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	legacy := &Conn{HardwareID: "legacy-refreeze", Send: make(chan []byte, 1)}
	if err := h.Register("3140088", legacy); err != nil {
		t.Fatal(err)
	}
	if h.Get("3140088") != nil {
		t.Fatal("legacy identity accepted after activation")
	}
	// Refreezing writes does not change the monotonic identity policy.
	if h.Get("3140088") != nil {
		t.Fatal("refreezing writes re-authorized legacy identity")
	}
}

func TestReviewPresenceAggregatesExcludeDelayedOldOwnerState(t *testing.T) {
	const number = "3140066"

	local := NewHub()
	local.SetLineResolver(func(string) (int64, error) { return 2, nil }, nil)
	local.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	stale := &Conn{
		LineID:       1,
		HardwareID:   "delayed-local-owner",
		ConnectionID: "old-local",
		LastSeen:     time.Unix(1234, 0),
		Send:         make(chan []byte, 1),
	}
	if err := local.Register(number, stale); err != nil {
		t.Fatal(err)
	}
	local.SetVoicemailUnheard(number, stale.HardwareID, 7)
	if got := local.OnlineNumbers(); slices.Contains(got, number) {
		t.Fatalf("delayed local owner appeared in online numbers: %v", got)
	}
	if got := local.LastSeenAt(number); got != nil {
		t.Fatalf("delayed local owner exposed last seen: %v", got)
	}
	if got := local.LineVoicemailUnheard(number); got != 0 {
		t.Fatalf("delayed local owner exposed %d unheard voicemails", got)
	}

	ds, _ := newTestDeviceState(t)
	ds.SetOnline(context.Background(), number, DevicePresence{
		LineID:       1,
		PodID:        ds.PodID(),
		HardwareID:   "delayed-redis-owner",
		ConnectionID: "old-redis",
	})
	remote := NewHub()
	remote.SetDeviceState(ds)
	remote.SetLineResolver(func(string) (int64, error) { return 2, nil }, nil)
	remote.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	if got := remote.OnlineNumbers(); slices.Contains(got, number) {
		t.Fatalf("delayed Redis owner appeared in online numbers: %v", got)
	}
	if got := remote.LastSeenAt(number); got != nil {
		t.Fatalf("delayed Redis owner exposed last seen: %v", got)
	}
}

func TestReviewPresenceAggregatesRejectZeroIDAfterCutover(t *testing.T) {
	const number = "3140067"

	local := NewHub()
	local.SetLineResolver(func(string) (int64, error) { return 2, nil }, nil)
	local.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	legacy := &Conn{
		HardwareID:   "legacy-local-owner",
		ConnectionID: "legacy-local",
		LastSeen:     time.Unix(5678, 0),
		Send:         make(chan []byte, 1),
	}
	if err := local.Register(number, legacy); err != nil {
		t.Fatal(err)
	}
	local.SetVoicemailUnheard(number, legacy.HardwareID, 9)
	if got := local.OnlineNumbers(); slices.Contains(got, number) {
		t.Fatalf("zero-ID local presence appeared in online numbers after cutover: %v", got)
	}
	if got := local.LastSeenAt(number); got != nil {
		t.Fatalf("zero-ID local presence exposed last seen after cutover: %v", got)
	}
	if got := local.LineVoicemailUnheard(number); got != 0 {
		t.Fatalf("zero-ID local presence exposed %d unheard voicemails after cutover", got)
	}

	ds, _ := newTestDeviceState(t)
	ds.SetOnline(context.Background(), number, DevicePresence{
		PodID:        ds.PodID(),
		HardwareID:   "legacy-redis-owner",
		ConnectionID: "legacy-redis",
	})
	remote := NewHub()
	remote.SetDeviceState(ds)
	remote.SetLineResolver(func(string) (int64, error) { return 2, nil }, nil)
	remote.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	if got := remote.OnlineNumbers(); slices.Contains(got, number) {
		t.Fatalf("zero-ID Redis presence appeared in online numbers after cutover: %v", got)
	}
	if got := remote.LastSeenAt(number); got != nil {
		t.Fatalf("zero-ID Redis presence exposed last seen after cutover: %v", got)
	}
}

func TestKnownLineSendCannotReachReusedNumberOwner(t *testing.T) {
	const number = "3140042"
	h := NewHub()
	h.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	newOwner := &Conn{LineID: 2, HardwareID: "replacement-owner", Send: make(chan []byte, 1)}
	if err := h.Register(number, newOwner); err != nil {
		t.Fatal(err)
	}

	err := h.SendToLine(number, 1, &Message{Type: TypeRestart})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("SendToLine error = %v, want ErrNotConnected", err)
	}
	select {
	case data := <-newOwner.Send:
		t.Fatalf("known-line send reached replacement owner: %q", data)
	default:
	}
}

func TestKnownLineSendRejectsZeroIdentityAfterCutover(t *testing.T) {
	const number = "3140043"
	h := NewHub()
	h.SetLineResolver(func(string) (int64, error) { return 2, nil }, nil)
	h.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	legacy := &Conn{HardwareID: "legacy-owner", Send: make(chan []byte, 1)}
	if err := h.Register(number, legacy); err != nil {
		t.Fatal(err)
	}

	if err := h.SendToLine(number, 0, &Message{Type: TypeLineSettings}); err == nil {
		t.Fatal("zero-ID known-line send succeeded after cutover")
	}
	select {
	case data := <-legacy.Send:
		t.Fatalf("zero-ID socket received settings after cutover: %q", data)
	default:
	}
	for _, identity := range h.LocalLineIdentities() {
		if identity.Number == number && identity.LineID == 0 {
			t.Fatal("scheduler identity snapshot included zero-ID socket after cutover")
		}
	}
}
