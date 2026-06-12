// Package calls tracks active and historical calls and conferences. The
// Tracker type is the central coordinator: it maintains an in-memory map of
// active 2-party calls, delegates 3-way conference state to ConferenceTracker,
// and persists all lifecycle events (initiated, answered, ended, conference
// created/ended) to the database. Optional observers (dashboard broadcaster,
// health store, relay) are wired in at startup via Set* methods.
package calls

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ConferenceRole classifies a participant's position within a three-way call.
type ConferenceRole int

const (
	ConferenceRoleHost ConferenceRole = iota
	ConferenceRoleAdded
)

// ConferenceState represents the lifecycle stage of a conference.
type ConferenceState int

const (
	ConferenceStateActive ConferenceState = iota
	ConferenceStateEnded
)

// ConferenceMember describes one participant in a conference, including when
// they joined and, if applicable, when and why they left.
type ConferenceMember struct {
	Phone      string
	Role       ConferenceRole
	JoinedAt   time.Time
	LeftAt     *time.Time
	LeftReason string
}

// Conference is the in-memory record of an active or recently ended three-way
// call. Members maps phone number to participant state.
type Conference struct {
	ID                uuid.UUID
	Host              string
	OriginatingCallID int64
	Members           map[string]*ConferenceMember
	State             ConferenceState
	CreatedAt         time.Time
	EndedAt           *time.Time
	EndReason         string
}

// ConferenceTracker manages the set of active conferences in memory. The
// memberIndex enforces the invariant that a phone can only be in one active
// conference at a time.
type ConferenceTracker struct {
	mu          sync.Mutex
	active      map[uuid.UUID]*Conference
	memberIndex map[string]uuid.UUID // phone -> conference id (active only)
	state       *ConfState
}

// NewConferenceTracker returns a ConferenceTracker with empty state.
func NewConferenceTracker() *ConferenceTracker {
	return &ConferenceTracker{
		active:      make(map[uuid.UUID]*Conference),
		memberIndex: make(map[string]uuid.UUID),
	}
}

// SetConfState attaches a Redis-backed conference state helper to the tracker.
func (ct *ConferenceTracker) SetConfState(cs *ConfState) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.state = cs
}

var (
	ErrInvalidConferenceSize     = errors.New("conference requires exactly 3 members (host + 2 added)")
	ErrMemberAlreadyInConference = errors.New("member already in an active conference")
	ErrHostAlreadyHosting        = errors.New("host already has an active conference")
	ErrConferenceNotFound        = errors.New("conference not found")
)

// CreateConference builds a 3-party conference with host + added members.
// Returns an error if the cap is exceeded, the host is already hosting,
// or any member is already in another active conference.
func (ct *ConferenceTracker) CreateConference(ctx context.Context, host string, originatingCallID int64, addedMembers []string) (*Conference, error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if 1+len(addedMembers) != 3 {
		return nil, ErrInvalidConferenceSize
	}
	if _, ok := ct.memberIndex[host]; ok {
		return nil, ErrHostAlreadyHosting
	}
	for _, m := range addedMembers {
		if _, ok := ct.memberIndex[m]; ok {
			return nil, fmt.Errorf("%w: %s", ErrMemberAlreadyInConference, m)
		}
	}

	now := time.Now()
	conf := &Conference{
		ID:                uuid.New(),
		Host:              host,
		OriginatingCallID: originatingCallID,
		Members:           make(map[string]*ConferenceMember, 3),
		State:             ConferenceStateActive,
		CreatedAt:         now,
	}
	conf.Members[host] = &ConferenceMember{Phone: host, Role: ConferenceRoleHost, JoinedAt: now}
	for _, m := range addedMembers {
		conf.Members[m] = &ConferenceMember{Phone: m, Role: ConferenceRoleAdded, JoinedAt: now}
	}
	ct.active[conf.ID] = conf
	ct.memberIndex[host] = conf.ID
	for _, m := range addedMembers {
		ct.memberIndex[m] = conf.ID
	}

	if ct.state != nil {
		allMembers := append([]string{host}, addedMembers...)
		ct.state.Create(ctx, conf.ID, host, originatingCallID, allMembers)
	}

	return conf, nil
}

// IsBusy returns true if phone is an active member of any active conference.
func (ct *ConferenceTracker) IsBusy(ctx context.Context, phone string) bool {
	ct.mu.Lock()
	if _, ok := ct.memberIndex[phone]; ok {
		ct.mu.Unlock()
		return true
	}
	state := ct.state
	ct.mu.Unlock()
	if state != nil {
		return state.IsBusy(ctx, phone)
	}
	return false
}

// ConferenceByPhone returns the active conference for a phone, or nil. The
// returned value is a deep copy: callers read it after the tracker mutex is
// released while other methods mutate the live conference under lock, so
// handing back the live pointer would be a data race.
func (ct *ConferenceTracker) ConferenceByPhone(ctx context.Context, phone string) *Conference {
	ct.mu.Lock()
	id, ok := ct.memberIndex[phone]
	if ok {
		conf := ct.snapshotLocked(id)
		ct.mu.Unlock()
		return conf
	}
	state := ct.state
	ct.mu.Unlock()
	if state != nil {
		return state.ConferenceByPhone(ctx, phone)
	}
	return nil
}

// ConferenceContains reports whether both phones are members of the same active conference.
func (ct *ConferenceTracker) ConferenceContains(ctx context.Context, confID uuid.UUID, phoneA, phoneB string) bool {
	ct.mu.Lock()
	conf, ok := ct.active[confID]
	if ok && conf.State == ConferenceStateActive {
		_, hasA := conf.Members[phoneA]
		_, hasB := conf.Members[phoneB]
		ct.mu.Unlock()
		return hasA && hasB
	}
	state := ct.state
	ct.mu.Unlock()
	if state != nil {
		return state.Contains(ctx, confID, phoneA, phoneB)
	}
	return false
}

// DropMember removes a single member. Returns the remaining member list and
// whether the conference ended as a result. In v1, any drop ends the conference
// (we cap at exactly 3, so dropping to 2 terminates).
func (ct *ConferenceTracker) DropMember(ctx context.Context, confID uuid.UUID, phone, reason string) (remaining []string, ended bool, err error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	conf, ok := ct.active[confID]
	if !ok {
		return nil, false, ErrConferenceNotFound
	}
	m, ok := conf.Members[phone]
	if !ok {
		return nil, false, fmt.Errorf("phone %s not in conference", phone)
	}
	now := time.Now()
	m.LeftAt = &now
	m.LeftReason = reason
	delete(ct.memberIndex, phone)

	for p := range conf.Members {
		if conf.Members[p].LeftAt == nil {
			remaining = append(remaining, p)
		}
	}
	// v1: any single drop ends the conference (3 -> 2)
	conf.State = ConferenceStateEnded
	conf.EndedAt = &now
	conf.EndReason = "member_left"
	delete(ct.active, confID)
	for _, p := range remaining {
		delete(ct.memberIndex, p)
	}

	if ct.state != nil {
		ct.state.RemoveMember(ctx, confID, phone)
		ct.state.End(ctx, confID, remaining)
	}

	return remaining, true, nil
}

// Snapshot returns a copy of the active conference with the given ID, or nil
// if no active conference has that ID. The returned pointer and its Members
// map are independent of the tracker's internal state and safe for the caller
// to iterate without holding the tracker's mutex.
func (ct *ConferenceTracker) Snapshot(id uuid.UUID) *Conference {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.snapshotLocked(id)
}

// snapshotLocked deep-copies the active conference with the given ID, or
// returns nil if none exists. Callers must hold ct.mu.
func (ct *ConferenceTracker) snapshotLocked(id uuid.UUID) *Conference {
	c, ok := ct.active[id]
	if !ok {
		return nil
	}
	cp := *c
	cp.Members = make(map[string]*ConferenceMember, len(c.Members))
	for k, m := range c.Members {
		mm := *m
		cp.Members[k] = &mm
	}
	return &cp
}

// EndConference ends the conference with the given reason. Returns the list of
// members that were still active at end-time.
func (ct *ConferenceTracker) EndConference(ctx context.Context, confID uuid.UUID, reason string) ([]string, error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	conf, ok := ct.active[confID]
	if !ok {
		return nil, ErrConferenceNotFound
	}
	now := time.Now()
	var active []string
	for p, m := range conf.Members {
		if m.LeftAt == nil {
			m.LeftAt = &now
			m.LeftReason = reason
			active = append(active, p)
		}
		delete(ct.memberIndex, p)
	}
	conf.State = ConferenceStateEnded
	conf.EndedAt = &now
	conf.EndReason = reason
	delete(ct.active, confID)

	if ct.state != nil {
		ct.state.End(ctx, confID, active)
	}

	return active, nil
}
