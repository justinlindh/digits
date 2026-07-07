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
	slog.InfoContext(ctx, "conference: merge requested", "host", host, "held", held, "active", active)

	// 1. Validate: host is in both calls.
	if !r.Tracker.InCall(ctx, host, held) || !r.Tracker.InCall(ctx, host, active) {
		slog.WarnContext(ctx, "conference: merge rejected", "host", host, "reason", "host_not_in_both_calls", "held", held, "active", active)
		r.sendRejection(host, msg.ConfID, "host_not_in_both_calls")
		return
	}

	// 2. Validate: neither added member is already in a conference.
	for _, p := range []string{held, active} {
		if r.Tracker.Conferences().IsBusy(ctx, p) {
			slog.WarnContext(ctx, "conference: merge rejected", "host", host, "reason", "member_already_in_conference", "busy_member", p)
			r.sendRejection(host, msg.ConfID, "member_already_in_conference")
			return
		}
	}

	// 3. Look up the originating call id (A-held).
	callID := r.Tracker.CallIDForPair(ctx, host, held)
	if callID == 0 {
		slog.WarnContext(ctx, "conference: merge rejected", "host", host, "reason", "call_id_unknown", "held", held)
		r.sendRejection(host, msg.ConfID, "call_id_unknown")
		return
	}

	conf, err := r.Tracker.CreateConferencePersistent(ctx, host, callID, []string{held, active})
	if err != nil {
		slog.ErrorContext(ctx, "create conference", "err", err)
		r.sendRejection(host, msg.ConfID, "create_failed")
		return
	}
	slog.InfoContext(ctx, "conference: created", "conf_id", conf.ID.String(), "host", host, "held", held, "active", active, "originating_call_id", callID)

	// 4. Notify all three members of the membership snapshot.
	members := []ConferenceMemberInfo{
		{Phone: host, Role: roleHost},
		{Phone: held, Role: roleAdded},
		{Phone: active, Role: roleAdded},
	}
	memberMsg := &Message{Type: TypeConferenceMember, ConfID: conf.ID.String(), Members: members}
	for _, m := range members {
		r.sendConf(ctx, m.Phone, memberMsg, "ConferenceMember")
	}
	slog.InfoContext(ctx, "conference: ConferenceMember broadcast", "conf_id", conf.ID.String(), "members", len(members))

	// 5. Instruct held and active peers to open a peer connection to each other.
	//    Deterministic tiebreak: numerically smaller phone number is the initiator.
	heldIsInitiator := held < active
	r.sendConf(ctx, held, &Message{
		Type:      TypeConferenceConnect,
		ConfID:    conf.ID.String(),
		Peer:      active,
		Initiator: heldIsInitiator,
	}, "ConferenceConnect")
	r.sendConf(ctx, active, &Message{
		Type:      TypeConferenceConnect,
		ConfID:    conf.ID.String(),
		Peer:      held,
		Initiator: !heldIsInitiator,
	}, "ConferenceConnect")
	slog.InfoContext(ctx, "conference: ConferenceConnect dispatched", "conf_id", conf.ID.String(), "held", held, "active", active, "held_is_initiator", heldIsInitiator)
}

func (r *Relay) sendRejection(host, confID, reason string) {
	_ = r.Hub.SendTo(host, &Message{
		Type:   TypeConferenceRejected,
		ConfID: confID,
		Reason: reason,
	})
}

// sendConf delivers a conference control message to one member. A failed send
// is expected (the member may have just disconnected) and is logged as a
// non-fatal warning with a stable shape; what names the message for the log
// line (e.g. "ConferenceEnd").
func (r *Relay) sendConf(ctx context.Context, phone string, msg *Message, what string) {
	if err := r.Hub.SendTo(phone, msg); err != nil {
		slog.WarnContext(ctx, "conference: "+what+" send failed", "conf_id", msg.ConfID, "to", phone, "err", err)
	}
}

func (r *Relay) endConference(ctx context.Context, confID uuid.UUID, reason string) {
	conf := r.Tracker.Conferences().Snapshot(confID)
	if conf == nil {
		// Already ended (e.g. a second caller triggers endConference after a
		// member drop already ended it). No members to notify.
		slog.InfoContext(ctx, "conference: end skipped, already ended", "conf_id", confID.String(), "reason", reason)
		return
	}
	slog.InfoContext(ctx, "conference: ending", "conf_id", confID.String(), "reason", reason, "members", len(conf.Members))
	if err := r.Tracker.EndConferencePersistent(ctx, confID, reason); err != nil {
		slog.ErrorContext(ctx, "end conference persist", "err", err)
	}
	for p := range conf.Members {
		r.sendConf(ctx, p, &Message{
			Type:   TypeConferenceEnd,
			ConfID: confID.String(),
			Reason: reason,
		}, "ConferenceEnd")
	}
	slog.InfoContext(ctx, "conference: ConferenceEnd broadcast", "conf_id", confID.String(), "reason", reason)
}

func (r *Relay) dropMemberFromConference(ctx context.Context, confID uuid.UUID, phone, reason string) {
	conf := r.Tracker.Conferences().Snapshot(confID)
	if conf == nil {
		// Already ended; nothing to drop or notify.
		slog.InfoContext(ctx, "conference: drop skipped, already ended", "conf_id", confID.String(), "phone", phone, "reason", reason)
		return
	}
	slog.InfoContext(ctx, "conference: drop member", "conf_id", confID.String(), "phone", phone, "reason", reason)
	var others []string
	for p := range conf.Members {
		if p != phone {
			others = append(others, p)
		}
	}
	remaining, err := r.Tracker.DropMemberPersistent(ctx, confID, phone, reason)
	if err != nil {
		slog.ErrorContext(ctx, "drop member", "err", err)
		return
	}

	for _, p := range others {
		r.sendConf(ctx, p, &Message{
			Type:   TypeConferenceLeave,
			ConfID: confID.String(),
			Peer:   phone,
			Reason: reason,
		}, "ConferenceLeave")
	}
	slog.InfoContext(ctx, "conference: ConferenceLeave broadcast", "conf_id", confID.String(), "left", phone, "notified", others)
	// v1: any drop ends the conference; notify remaining members explicitly so
	// client controllers know to fully tear down (not just drop the leaver).
	for _, p := range remaining {
		r.sendConf(ctx, p, &Message{
			Type:   TypeConferenceEnd,
			ConfID: confID.String(),
			Reason: "member_left",
		}, "ConferenceEnd")
	}
	slog.InfoContext(ctx, "conference: ConferenceEnd broadcast after drop", "conf_id", confID.String(), "remaining", remaining)
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
	slog.InfoContext(ctx, "conference: kick member", "conf_id", confID.String(), "kicked", kickedPhone, "reason", reason)
	// Notify the kicked phone first so it starts tearing down before the
	// drop cascade reassigns the surviving pair to a continuation call.
	// Non-member or unregistered phones get a no-op send; the caller is
	// responsible for pre-validating membership.
	r.sendConf(ctx, kickedPhone, &Message{
		Type:   TypeConferenceEnd,
		ConfID: confID.String(),
		Reason: reason,
	}, "kick ConferenceEnd")
	r.dropMemberFromConference(ctx, confID, kickedPhone, reason)
}
