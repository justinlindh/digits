package wififallback

import (
	"errors"
	"reflect"
	"testing"
)

func TestNmcliCheckerParses(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		runErr  error
		want    bool
		wantErr bool
	}{
		{"connected full", "full\n", nil, true, false},
		{"connected limited", "limited\n", nil, false, false},
		{"none", "none\n", nil, false, false},
		{"portal", "portal\n", nil, false, false},
		{"unknown", "unknown\n", nil, false, false},
		{"trailing whitespace", "  full  \n", nil, true, false},
		{"uppercase FULL", "FULL\n", nil, true, false},
		{"command error", "", errors.New("boom"), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &nmcliChecker{run: func(args ...string) ([]byte, error) {
				if tc.runErr != nil {
					return nil, tc.runErr
				}
				return []byte(tc.output), nil
			}}
			got, err := c.HasConnectivity()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("HasConnectivity() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNmcliCheckerUsesTerseFormat(t *testing.T) {
	var gotArgs []string
	c := &nmcliChecker{run: func(args ...string) ([]byte, error) {
		gotArgs = append([]string(nil), args...)
		return []byte("full\n"), nil
	}}
	if _, err := c.HasConnectivity(); err != nil {
		t.Fatalf("HasConnectivity: %v", err)
	}
	want := []string{"-t", "-f", "CONNECTIVITY", "general"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}
