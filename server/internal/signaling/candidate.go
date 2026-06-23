package signaling

import (
	"strings"
)

// SDPSummary is a non-sensitive shape summary of an SDP offer or answer. It
// records structural counts an operator needs to confirm a session description
// was relayed and is well-formed (right number of media sections, whether ICE
// candidates were bundled in-band) without retaining any of the SDP body,
// which can contain fingerprints, ufrag/pwd, and address information.
type SDPSummary struct {
	// MLines is the number of "m=" media descriptions (typically 1 for a
	// single audio session).
	MLines int
	// Candidates is the number of "a=candidate:" attributes embedded in the
	// SDP. Digits trickles ICE, so this is usually 0; a non-zero count means
	// the peer bundled candidates into the description.
	Candidates int
	// Bytes is the length of the SDP body, a coarse size signal that never
	// echoes content.
	Bytes int
}

// SummarizeSDP returns a content-free summary of an SDP body. It counts the
// "m=" and "a=candidate:" lines and the total length. It never returns any
// substring of the input, so the result is safe to log at Info.
func SummarizeSDP(sdp string) SDPSummary {
	s := SDPSummary{Bytes: len(sdp)}
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "m="):
			s.MLines++
		case strings.HasPrefix(line, "a=candidate:"):
			s.Candidates++
		}
	}
	return s
}

// CandidateInfo is a non-sensitive summary of an ICE candidate parsed from the
// SDP candidate attribute carried in a signaling "ice" message. It captures the
// fields an operator needs to reason about media connectivity (is the pair
// host/srflx/relay, UDP or TCP, what address:port) without retaining the full
// candidate line. The address and port describe a network endpoint, not user
// identity, and are logged at Debug to keep per-candidate volume off the
// default Info stream.
type CandidateInfo struct {
	// Type is the ICE candidate type: "host", "srflx", "prflx", or "relay"
	// per RFC 8445. Empty when the candidate line could not be parsed.
	Type string
	// Transport is the candidate transport, lowercased: "udp" or "tcp".
	Transport string
	// Address is the connection address (IPv4, IPv6, or FQDN) of the candidate.
	Address string
	// Port is the connection port as it appeared on the wire (string to avoid
	// implying numeric validation the relay does not perform).
	Port string
	// RelatedAddress and RelatedPort are the raddr/rport of a reflexive or
	// relay candidate (the base/server-reflexive address it was derived from),
	// empty for host candidates and when absent from the line.
	RelatedAddress string
	RelatedPort    string
}

// Parsed reports whether the candidate line yielded the core fields (type,
// transport, address, port). A false result means the string was empty,
// malformed, or an end-of-candidates marker, and the caller should log it as
// unparseable rather than emit misleading zero-value fields.
func (c CandidateInfo) Parsed() bool {
	return c.Type != "" && c.Transport != "" && c.Address != "" && c.Port != ""
}

// ParseCandidate extracts a non-sensitive summary from an ICE candidate
// attribute string as it arrives on the wire in Message.Candidate. The input
// is the value pion produces from ICECandidate.ToJSON().Candidate, which is the
// SDP "candidate:" attribute. The leading "candidate:" token and any "a="
// prefix are tolerated but optional.
//
// The grammar (RFC 8839 / RFC 5245) is:
//
//	[a=][candidate:]<foundation> <component-id> <transport> <priority>
//	    <connection-address> <port> typ <cand-type>
//	    [raddr <rel-addr> rport <rel-port>] *(<ext-name> <ext-value>)
//
// Parsing is deliberately lenient: an empty string, an end-of-candidates
// marker, or any line missing the fixed positional fields returns a zero
// CandidateInfo whose Parsed() reports false. ParseCandidate never panics on
// malformed input and never returns an error, so callers can log the summary
// unconditionally.
func ParseCandidate(s string) CandidateInfo {
	s = strings.TrimSpace(s)
	if s == "" {
		return CandidateInfo{}
	}
	// Tolerate a leading "a=" (full SDP line) and the "candidate:" attribute
	// name; pion omits both, but defensive callers and other stacks may not.
	s = strings.TrimPrefix(s, "a=")
	s = strings.TrimPrefix(s, "candidate:")

	fields := strings.Fields(s)
	// The fixed prefix is 8 tokens: foundation, component, transport, priority,
	// address, port, the literal "typ", and the candidate type. Anything
	// shorter cannot be a well-formed candidate.
	if len(fields) < 8 {
		return CandidateInfo{}
	}

	info := CandidateInfo{
		Transport: strings.ToLower(fields[2]),
		Address:   fields[4],
		Port:      fields[5],
	}
	// fields[6] must be the literal "typ"; fields[7] is the candidate type.
	if !strings.EqualFold(fields[6], "typ") {
		return CandidateInfo{}
	}
	info.Type = strings.ToLower(fields[7])

	// Scan the optional trailing token pairs for raddr/rport. Stop at the
	// first non-pair remainder to stay robust against vendor extensions that
	// append a lone token.
	for i := 8; i+1 < len(fields); i += 2 {
		switch strings.ToLower(fields[i]) {
		case "raddr":
			info.RelatedAddress = fields[i+1]
		case "rport":
			info.RelatedPort = fields[i+1]
		}
	}

	return info
}
