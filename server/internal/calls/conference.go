package calls

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ConferenceRole int

const (
	ConferenceRoleHost  ConferenceRole = iota
	ConferenceRoleAdded ConferenceRole = iota
)

type ConferenceState int

const (
	ConferenceStateActive ConferenceState = iota
	ConferenceStateEnded  ConferenceState = iota
)

type ConferenceMember struct {
	Phone      string
	Role       ConferenceRole
	JoinedAt   time.Time
	LeftAt     *time.Time
	LeftReason string
}

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

type ConferenceTracker struct {
	mu          sync.Mutex
	active      map[uuid.UUID]*Conference
	memberIndex map[string]uuid.UUID // phone -> conference id (active only)
}

func NewConferenceTracker() *ConferenceTracker {
	return &ConferenceTracker{
		active:      make(map[uuid.UUID]*Conference),
		memberIndex: make(map[string]uuid.UUID),
	}
}

var (
	ErrConferenceCapExceeded     = errors.New("conference cap of 3 exceeded")
	ErrMemberAlreadyInConference = errors.New("member already in an active conference")
	ErrHostAlreadyHosting        = errors.New("host already has an active conference")
	ErrConferenceNotFound        = errors.New("conference not found")
)

// CreateConference builds a 3-party conference with host + added members.
// Returns an error if the cap is exceeded, the host is already hosting,
// or any member is already in another active conference.
func (ct *ConferenceTracker) CreateConference(host string, originatingCallID int64, addedMembers []string) (*Conference, error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if 1+len(addedMembers) != 3 {
		return nil, ErrConferenceCapExceeded
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
	return conf, nil
}

// IsBusy returns true if phone is an active member of any active conference.
func (ct *ConferenceTracker) IsBusy(phone string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	_, ok := ct.memberIndex[phone]
	return ok
}

// ConferenceByPhone returns the active conference for a phone, or nil.
func (ct *ConferenceTracker) ConferenceByPhone(phone string) *Conference {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	id, ok := ct.memberIndex[phone]
	if !ok {
		return nil
	}
	return ct.active[id]
}

// ConferenceContains reports whether both phones are members of the same active conference.
func (ct *ConferenceTracker) ConferenceContains(confID uuid.UUID, phoneA, phoneB string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	conf, ok := ct.active[confID]
	if !ok || conf.State != ConferenceStateActive {
		return false
	}
	_, hasA := conf.Members[phoneA]
	_, hasB := conf.Members[phoneB]
	return hasA && hasB
}

// DropMember removes a single member. Returns the remaining member list and
// whether the conference ended as a result. In v1, any drop ends the conference
// (we cap at exactly 3, so dropping to 2 terminates).
func (ct *ConferenceTracker) DropMember(confID uuid.UUID, phone, reason string) (remaining []string, ended bool, err error) {
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
	return remaining, true, nil
}

// EndConference ends the conference with the given reason. Returns the list of
// members that were still active at end-time.
func (ct *ConferenceTracker) EndConference(confID uuid.UUID, reason string) ([]string, error) {
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
	return active, nil
}
