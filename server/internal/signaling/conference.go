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

	// 1. Validate: host is in both calls.
	if !r.Tracker.InCall(ctx, host, held) || !r.Tracker.InCall(ctx, host, active) {
		r.sendRejection(ctx, host, msg.ConfID, "host_not_in_both_calls")
		return
	}

	// 2. Validate: neither added member is already in a conference.
	for _, p := range []string{held, active} {
		if r.Tracker.Conferences().IsBusy(p) {
			r.sendRejection(ctx, host, msg.ConfID, "member_already_in_conference")
			return
		}
	}

	// 3. Look up the originating call id (A-held).
	callID := r.Tracker.CallIDForPair(ctx, host, held)
	if callID == 0 {
		r.sendRejection(ctx, host, msg.ConfID, "call_id_unknown")
		return
	}

	conf, err := r.Tracker.CreateConferencePersistent(ctx, host, callID, []string{held, active})
	if err != nil {
		slog.Error("create conference", "err", err)
		r.sendRejection(ctx, host, msg.ConfID, "create_failed")
		return
	}

	// 4. Notify all three members of the membership snapshot.
	members := []ConferenceMemberInfo{
		{Phone: host, Role: RoleHost},
		{Phone: held, Role: RoleAdded},
		{Phone: active, Role: RoleAdded},
	}
	memberMsg := &Message{Type: TypeConferenceMember, ConfID: conf.ID.String(), Members: members}
	for _, m := range members {
		_ = r.Hub.SendTo(m.Phone, memberMsg)
	}

	// 5. Instruct held and active peers to open a peer connection to each other.
	//    Deterministic tiebreak: numerically smaller phone number is the initiator.
	heldIsInitiator := held < active
	_ = r.Hub.SendTo(held, &Message{
		Type:      TypeConferenceConnect,
		ConfID:    conf.ID.String(),
		Peer:      active,
		Initiator: heldIsInitiator,
	})
	_ = r.Hub.SendTo(active, &Message{
		Type:      TypeConferenceConnect,
		ConfID:    conf.ID.String(),
		Peer:      held,
		Initiator: !heldIsInitiator,
	})
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
		return
	}
	if err := r.Tracker.EndConferencePersistent(ctx, confID, reason); err != nil {
		slog.Error("end conference persist", "err", err)
	}
	for p := range conf.Members {
		_ = r.Hub.SendTo(p, &Message{
			Type:   TypeConferenceEnd,
			ConfID: confID.String(),
			Reason: reason,
		})
	}
}

func (r *Relay) dropMemberFromConference(ctx context.Context, confID uuid.UUID, phone, reason string) {
	conf := r.Tracker.Conferences().Snapshot(confID)
	if conf == nil {
		// Already ended; nothing to drop or notify.
		return
	}
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
		_ = r.Hub.SendTo(p, &Message{
			Type:   TypeConferenceLeave,
			ConfID: confID.String(),
			Peer:   phone,
			Reason: reason,
		})
	}
	// v1: any drop ends the conference; notify remaining members explicitly so
	// client controllers know to fully tear down (not just drop the leaver).
	for _, p := range remaining {
		_ = r.Hub.SendTo(p, &Message{
			Type:   TypeConferenceEnd,
			ConfID: confID.String(),
			Reason: "member_left",
		})
	}
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
	// Notify the kicked phone first so it starts tearing down before the
	// drop cascade reassigns the surviving pair to a continuation call.
	// Non-member or unregistered phones get a no-op send; the caller is
	// responsible for pre-validating membership.
	if err := r.Hub.SendTo(kickedPhone, &Message{
		Type:   TypeConferenceEnd,
		ConfID: confID.String(),
		Reason: reason,
	}); err != nil {
		slog.Debug("kick: send ConferenceEnd to kicked phone failed", "phone", kickedPhone, "err", err)
	}
	r.dropMemberFromConference(ctx, confID, kickedPhone, reason)
}
