package signaling

import (
	"encoding/json"
	"testing"
)

func TestMessageConferenceFieldsRoundTrip(t *testing.T) {
	in := Message{
		Type:   TypeConferenceMember,
		ConfID: "abc-123",
		Members: []ConferenceMemberInfo{
			{Phone: "5550001", Role: "host"},
			{Phone: "5550002", Role: "added"},
		},
	}
	b, err := json.Marshal(&in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Type != in.Type || out.ConfID != in.ConfID || len(out.Members) != 2 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	if out.Members[0].Phone != "5550001" || out.Members[0].Role != "host" {
		t.Fatalf("member 0 mismatch: %+v", out.Members[0])
	}
	if out.Members[1].Phone != "5550002" || out.Members[1].Role != "added" {
		t.Fatalf("member 1 mismatch: %+v", out.Members[1])
	}
}

func TestMessageConferenceFieldsOmitempty(t *testing.T) {
	// A Message with no conference fields should not include any conf_* keys in its JSON output.
	in := Message{Type: TypeCall, From: "5550001", To: "5550002"}
	b, err := json.Marshal(&in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, substr := range []string{"conf_id", "held_peer", "active_peer", "members", "peer\":", "initiator"} {
		if contains(s, substr) {
			t.Fatalf("expected %q absent from JSON but got: %s", substr, s)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
