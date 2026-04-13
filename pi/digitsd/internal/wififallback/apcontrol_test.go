package wififallback

import (
	"errors"
	"reflect"
	"testing"
)

func TestScriptAPControllerInvokesCommands(t *testing.T) {
	var calls [][]string
	ctl := &scriptAPController{run: func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}}

	if err := ctl.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := ctl.Down(); err != nil {
		t.Fatalf("Down: %v", err)
	}

	want := [][]string{
		{"/usr/local/bin/digits-ap-check", "up"},
		{"/usr/local/bin/digits-ap-check", "down"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

func TestScriptAPControllerPropagatesError(t *testing.T) {
	ctl := &scriptAPController{run: func(args ...string) error {
		return errors.New("script failed")
	}}
	if err := ctl.Up(); err == nil {
		t.Error("expected error from Up")
	}
	if err := ctl.Down(); err == nil {
		t.Error("expected error from Down")
	}
}

func TestScriptAPControllerHasClient(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{"empty", "", false},
		{"one station", "Station aa:bb:cc:dd:ee:ff (on wlan0)\n\tinactive time: 100 ms\n", true},
		{"whitespace only", "   \n\n", false},
		{"multiple stations", "Station aa:bb:cc:dd:ee:ff (on wlan0)\nStation 11:22:33:44:55:66 (on wlan0)\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctl := &scriptAPController{
				runOut: func(args ...string) ([]byte, error) {
					return []byte(tc.output), nil
				},
			}
			got, err := ctl.HasClient()
			if err != nil {
				t.Fatalf("HasClient: %v", err)
			}
			if got != tc.want {
				t.Errorf("HasClient() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScriptAPControllerHasClientPropagatesError(t *testing.T) {
	ctl := &scriptAPController{
		runOut: func(args ...string) ([]byte, error) {
			return nil, errors.New("iw failed")
		},
	}
	if _, err := ctl.HasClient(); err == nil {
		t.Error("expected error from HasClient")
	}
}

func TestScriptAPControllerHasClientUsesCorrectArgs(t *testing.T) {
	var gotArgs []string
	ctl := &scriptAPController{
		runOut: func(args ...string) ([]byte, error) {
			gotArgs = append([]string(nil), args...)
			return []byte(""), nil
		},
	}
	if _, err := ctl.HasClient(); err != nil {
		t.Fatalf("HasClient: %v", err)
	}
	want := []string{"iw", "dev", "wlan0", "station", "dump"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}
