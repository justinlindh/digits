package audio

import (
	"testing"
)

// loadCharacter returns the current character chain (may be nil).
func (p *Pipeline) loadCharacter() *BiquadChain {
	return p.character.Load()
}

func TestSetVoiceStyleTogglesCharacterChain(t *testing.T) {
	p := NewPipeline(DefaultPipelineConfig())

	// Default should be copper (character filter present).
	if p.loadCharacter() == nil {
		t.Fatal("default pipeline: character chain should be non-nil (copper)")
	}

	p.SetVoiceStyle("modern")
	if p.loadCharacter() != nil {
		t.Errorf("after modern: character chain should be nil, got non-nil")
	}

	p.SetVoiceStyle("copper")
	if p.loadCharacter() == nil {
		t.Errorf("after copper: character chain should be non-nil")
	}
}

func TestSetVoiceStyleUnknownFallsBackToCopper(t *testing.T) {
	p := NewPipeline(DefaultPipelineConfig())
	p.SetVoiceStyle("modern")
	p.SetVoiceStyle("bogus")
	if p.loadCharacter() == nil {
		t.Errorf("unknown style: should fall back to copper (non-nil), got nil")
	}
}
