package signaling

import (
	"log/slog"

	"github.com/google/uuid"
)

// handleConferenceMerge is invoked when a host (A) flashes to merge its two
// active 2-party calls (A-B, A-C) into a 3-party conference.
func (r *Relay) handleConferenceMerge(host string, msg *Message) {
	held := msg.HeldPeer
	active := msg.ActivePeer

	// 1. Validate: host is in both calls.
	if !r.Tracker.InCall(host, held) || !r.Tracker.InCall(host, active) {
		r.sendRejection(host, msg.ConfID, "host_not_in_both_calls")
		return
	}

	// 2. Validate: neither added member is already in a conference.
	for _, p := range []string{held, active} {
		if r.Tracker.Conferences().IsBusy(p) {
			r.sendRejection(host, msg.ConfID, "member_already_in_conference")
			return
		}
	}

	// 3. Look up the originating call id (A-held).
	callID := r.Tracker.CallIDFor(host, held)
	if callID == 0 {
		r.sendRejection(host, msg.ConfID, "call_id_unknown")
		return
	}

	conf, err := r.Tracker.CreateConferencePersistent(host, callID, []string{held, active})
	if err != nil {
		slog.Error("create conference", "err", err)
		r.sendRejection(host, msg.ConfID, "create_failed")
		return
	}

	// 4. Notify all three members of the membership snapshot.
	members := []ConferenceMemberInfo{
		{Phone: host, Role: "host"},
		{Phone: held, Role: "added"},
		{Phone: active, Role: "added"},
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

func (r *Relay) sendRejection(host, confID, reason string) {
	_ = r.Hub.SendTo(host, &Message{
		Type:   TypeConferenceRejected,
		ConfID: confID,
		Reason: reason,
	})
}

func (r *Relay) endConference(confID uuid.UUID, reason string) {
	conf := r.Tracker.Conferences().Snapshot(confID)
	if err := r.Tracker.EndConferencePersistent(confID, reason); err != nil {
		slog.Error("end conference persist", "err", err)
	}
	if conf == nil {
		return
	}
	for p := range conf.Members {
		_ = r.Hub.SendTo(p, &Message{
			Type:   TypeConferenceEnd,
			ConfID: confID.String(),
			Reason: reason,
		})
	}
}

func (r *Relay) dropMemberFromConference(confID uuid.UUID, phone, reason string) {
	conf := r.Tracker.Conferences().Snapshot(confID)
	var others []string
	if conf != nil {
		for p := range conf.Members {
			if p != phone {
				others = append(others, p)
			}
		}
	}
	remaining, _, err := r.Tracker.DropMemberPersistent(confID, phone, reason)
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
