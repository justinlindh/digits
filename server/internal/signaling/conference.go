package signaling

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// handleConferenceMerge is invoked when a host (A) flashes to merge its two
// active 2-party calls (A-B, A-C) into a 3-party conference.
func (r *Relay) handleConferenceMerge(ctx context.Context, host string, msg *Message) {
	held := msg.HeldPeer
	active := msg.ActivePeer
	slog.Info("conference: merge requested", "host", host, "held", held, "active", active)

	// 1. Validate: host is in both calls.
	if !r.Tracker.InCall(host, held) || !r.Tracker.InCall(host, active) {
		slog.Warn("conference: merge rejected", "host", host, "reason", "host_not_in_both_calls", "held", held, "active", active)
		r.sendRejection(ctx, host, msg.ConfID, "host_not_in_both_calls")
		return
	}

	// 2. Validate: neither added member is already in a conference.
	for _, p := range []string{held, active} {
		if r.Tracker.Conferences().IsBusy(p) {
			slog.Warn("conference: merge rejected", "host", host, "reason", "member_already_in_conference", "busy_member", p)
			r.sendRejection(ctx, host, msg.ConfID, "member_already_in_conference")
			return
		}
	}

	// 3. Look up the originating call id (A-held).
	callID := r.Tracker.CallIDForPair(host, held)
	if callID == 0 {
		slog.Warn("conference: merge rejected", "host", host, "reason", "call_id_unknown", "held", held)
		r.sendRejection(ctx, host, msg.ConfID, "call_id_unknown")
		return
	}

	conf, err := r.Tracker.CreateConferencePersistent(ctx, host, callID, []string{held, active})
	if err != nil {
		slog.Error("create conference", "err", err)
		r.sendRejection(ctx, host, msg.ConfID, "create_failed")
		return
	}
	slog.Info("conference: created", "conf_id", conf.ID.String(), "host", host, "held", held, "active", active, "originating_call_id", callID)

	// 4. Notify all three members of the membership snapshot.
	members := []ConferenceMemberInfo{
		{Phone: host, Role: RoleHost},
		{Phone: held, Role: RoleAdded},
		{Phone: active, Role: RoleAdded},
	}
	memberMsg := &Message{Type: TypeConferenceMember, ConfID: conf.ID.String(), Members: members}
	for _, m := range members {
		if err := r.Hub.SendTo(m.Phone, memberMsg); err != nil {
			slog.Warn("conference: ConferenceMember send failed", "conf_id", conf.ID.String(), "to", m.Phone, "err", err)
		}
	}
	slog.Info("conference: ConferenceMember broadcast", "conf_id", conf.ID.String(), "members", len(members))

	// 5. Instruct held and active peers to open a peer connection to each other.
	//    Deterministic tiebreak: numerically smaller phone number is the initiator.
	heldIsInitiator := held < active
	if err := r.Hub.SendTo(held, &Message{
		Type:      TypeConferenceConnect,
		ConfID:    conf.ID.String(),
		Peer:      active,
		Initiator: heldIsInitiator,
	}); err != nil {
		slog.Warn("conference: ConferenceConnect send failed", "conf_id", conf.ID.String(), "to", held, "err", err)
	}
	if err := r.Hub.SendTo(active, &Message{
		Type:      TypeConferenceConnect,
		ConfID:    conf.ID.String(),
		Peer:      held,
		Initiator: !heldIsInitiator,
	}); err != nil {
		slog.Warn("conference: ConferenceConnect send failed", "conf_id", conf.ID.String(), "to", active, "err", err)
	}
	slog.Info("conference: ConferenceConnect dispatched", "conf_id", conf.ID.String(), "held", held, "active", active, "held_is_initiator", heldIsInitiator)
}

func (r *Relay) sendRejection(ctx context.Context, host, confID, reason string) {
	_ = r.Hub.SendTo(host, &Message{
		Type:   TypeConferenceRejected,
		ConfID: confID,
		Reason: reason,
	})
}

func (r *Relay) endConference(ctx context.Context, confID uuid.UUID, reason string) {
	conf := r.Tracker.Conferences().Snapshot(confID)
	if conf == nil {
		// Already ended (e.g. a second caller triggers endConference after a
		// member drop already ended it). No members to notify.
		slog.Info("conference: end skipped, already ended", "conf_id", confID.String(), "reason", reason)
		return
	}
	slog.Info("conference: ending", "conf_id", confID.String(), "reason", reason, "members", len(conf.Members))
	if err := r.Tracker.EndConferencePersistent(ctx, confID, reason); err != nil {
		slog.Error("end conference persist", "err", err)
	}
	for p := range conf.Members {
		if err := r.Hub.SendTo(p, &Message{
			Type:   TypeConferenceEnd,
			ConfID: confID.String(),
			Reason: reason,
		}); err != nil {
			slog.Warn("conference: ConferenceEnd send failed", "conf_id", confID.String(), "to", p, "err", err)
		}
	}
	slog.Info("conference: ConferenceEnd broadcast", "conf_id", confID.String(), "reason", reason)
}

func (r *Relay) dropMemberFromConference(ctx context.Context, confID uuid.UUID, phone, reason string) {
	conf := r.Tracker.Conferences().Snapshot(confID)
	if conf == nil {
		// Already ended; nothing to drop or notify.
		slog.Info("conference: drop skipped, already ended", "conf_id", confID.String(), "phone", phone, "reason", reason)
		return
	}
	slog.Info("conference: drop member", "conf_id", confID.String(), "phone", phone, "reason", reason)
	var others []string
	for p := range conf.Members {
		if p != phone {
			others = append(others, p)
		}
	}
	remaining, _, err := r.Tracker.DropMemberPersistent(ctx, confID, phone, reason)
	if err != nil {
		slog.Error("drop member", "err", err)
		return
	}

	for _, p := range others {
		if err := r.Hub.SendTo(p, &Message{
			Type:   TypeConferenceLeave,
			ConfID: confID.String(),
			Peer:   phone,
			Reason: reason,
		}); err != nil {
			slog.Warn("conference: ConferenceLeave send failed", "conf_id", confID.String(), "to", p, "err", err)
		}
	}
	slog.Info("conference: ConferenceLeave broadcast", "conf_id", confID.String(), "left", phone, "notified", others)
	// v1: any drop ends the conference; notify remaining members explicitly so
	// client controllers know to fully tear down (not just drop the leaver).
	for _, p := range remaining {
		if err := r.Hub.SendTo(p, &Message{
			Type:   TypeConferenceEnd,
			ConfID: confID.String(),
			Reason: "member_left",
		}); err != nil {
			slog.Warn("conference: ConferenceEnd send failed", "conf_id", confID.String(), "to", p, "err", err)
		}
	}
	slog.Info("conference: ConferenceEnd broadcast after drop", "conf_id", confID.String(), "remaining", remaining)
}

// KickMember drops a member from the conference and notifies the kicked
// phone so it tears down its mesh cleanly. v1 conference semantics
// (DropMemberPersistent) end the whole conference on any drop, so
// remaining members receive TypeConferenceEnd via the existing
// dropMemberFromConference path. Kick adds one extra TypeConferenceEnd to
// the kicked phone itself.
//
// Reason is logged on the kicked phone's ConferenceEnd message; callers
// may pass any human-friendly string (e.g., "kicked by <display name>").
func (r *Relay) KickMember(ctx context.Context, confID uuid.UUID, kickedPhone, reason string) {
	slog.Info("conference: kick member", "conf_id", confID.String(), "kicked", kickedPhone, "reason", reason)
	// Notify the kicked phone first so it starts tearing down before the
	// drop cascade reassigns the surviving pair to a continuation call.
	// Non-member or unregistered phones get a no-op send; the caller is
	// responsible for pre-validating membership.
	if err := r.Hub.SendTo(kickedPhone, &Message{
		Type:   TypeConferenceEnd,
		ConfID: confID.String(),
		Reason: reason,
	}); err != nil {
		slog.Warn("conference: kick ConferenceEnd send to kicked phone failed", "conf_id", confID.String(), "phone", kickedPhone, "err", err)
	}
	r.dropMemberFromConference(ctx, confID, kickedPhone, reason)
}
