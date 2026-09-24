package main

import (
	"context"
	"net/netip"
	"os"
	"strings"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// decideLab crawls two devices and points NETMAPPER_DSN at the schema they live in, so the
// subcommand runs the way an operator runs it.
func decideLab(t *testing.T) (*Lab, []string) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("a", "S001"),
		Addr("10.0.0.2"): FakeOS("b", "S002"),
	}})
	doc := strings.Replace(Doc, "targets: [10.0.0.1]", "targets: [10.0.0.1, 10.0.0.2]", 1)
	l.Crawl(doc)

	schema := l.DB.Config().ConnConfig.RuntimeParams["search_path"]
	dsn := os.Getenv("NETMAPPER_TEST_DSN")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	t.Setenv("NETMAPPER_DSN", dsn+sep+"options=-c%20search_path%3D"+schema)

	keys := l.Strings(`SELECT key FROM device ORDER BY key`)
	if len(keys) != 2 {
		t.Fatalf("devices %v, want two to decide about", keys)
	}
	return l, keys
}

// T058, contracts/cli.md: recording a decision about something that does not exist is a typo, not an
// intention. Each of these is invalid input, not a runtime error.
func TestDecideRefusesWhatCannotBeTrue(t *testing.T) {
	l, keys := decideLab(t)
	ctx := context.Background()

	for _, c := range []struct {
		why  string
		args []string
	}{
		{"an unknown subject", []string{"merge", "--perimeter", "lab", "--keys", keys[0] + ",serial:NOPE"}},
		{"the same key twice", []string{"merge", "--perimeter", "lab", "--keys", keys[0] + "," + keys[0]}},
		{"a perimeter that knows neither key", []string{"merge", "--perimeter", "other", "--keys", keys[0] + "," + keys[1]}},
		{"an identifier attributed elsewhere", []string{"split", "--perimeter", "lab", "--key", keys[0], "--identifier", "serial:S002"}},
		{"an identifier that is not this device's", []string{"split", "--perimeter", "lab", "--key", keys[0], "--identifier", "serial=S002"}},
		{"no perimeter at all", []string{"merge", "--keys", keys[0] + "," + keys[1]}},
	} {
		if code := cmdDecide(ctx, c.args); code != exitInvalid {
			t.Errorf("%s: exit %d, want %d", c.why, code, exitInvalid)
		}
	}
	if n := l.Int(`SELECT count(*) FROM entity_decision`); n != 0 {
		t.Errorf("%d decisions recorded by invalid input, want none", n)
	}
}

// The other half of the contract: a decision an operator can mean is recorded, and recording it
// changes no entity until the snapshots carrying its subjects are resolved again (FR-009).
func TestDecideRecordsAndChangesNothingYet(t *testing.T) {
	l, keys := decideLab(t)
	ctx := context.Background()

	before := l.Int(`SELECT count(*) FROM entity`)
	if code := cmdDecide(ctx, []string{"merge", "--perimeter", "lab", "--keys", keys[0] + "," + keys[1],
		"--note", "warranty swap"}); code != exitOK {
		t.Fatalf("exit %d, want %d", code, exitOK)
	}
	rows := l.Strings(`SELECT kind || ' ' || array_to_string(subjects, ',') || ' ' || actor || ' ' || note FROM entity_decision`)
	if len(rows) != 1 || !strings.HasPrefix(rows[0], "merge "+keys[0]+","+keys[1]+" ") ||
		!strings.HasSuffix(rows[0], " warranty swap") {
		t.Errorf("decision %v, want the merge as it was recorded", rows)
	}
	if after := l.Int(`SELECT count(*) FROM entity`); after != before {
		t.Errorf("entities %d then %d: recording a decision must change none", before, after)
	}

	// A split naming an identifier the device really carries is valid input.
	if code := cmdDecide(ctx, []string{"split", "--perimeter", "lab", "--key", keys[0],
		"--identifier", "chassis_mac=" + strings.TrimPrefix(keys[0], "chassis_mac:")}); code != exitOK {
		t.Errorf("split exit %d, want %d", code, exitOK)
	}
}
