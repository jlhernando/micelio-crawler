package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A failed check command's output (it may print secrets) stays out of the
// reason, which is saved in the schedule state and served by the API.
func TestPrecheckCommandOutputNotInReason(t *testing.T) {
	old := runCommand
	defer func() { runCommand = old }()
	runCommand = func(ctx context.Context, name string, args ...string) (string, error) {
		return "token=CMD-SECRET\n", errors.New("exit status 1")
	}
	r := (&Precheck{Command: "check"}).Run(context.Background())
	if r.OK || r.Reason != "check command failed: exit status 1" || r.Output != "token=CMD-SECRET" {
		t.Fatalf("result = %+v", r)
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "CMD-SECRET") {
		t.Errorf("JSON carries the output: %s", data)
	}
}
