package migrations

import (
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var fileName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.(up|down)\.sql$`)

type migration struct {
	version  int
	name     string
	up, down string
}

// load reads every migration file and groups it by version.
func load(t *testing.T) []migration {
	t.Helper()
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	byVersion := map[int]*migration{}
	for _, e := range entries {
		m := fileName.FindStringSubmatch(e.Name())
		if m == nil {
			t.Errorf("%s does not match NNNN_description.(up|down).sql", e.Name())
			continue
		}
		v, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(FS, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		mig := byVersion[v]
		if mig == nil {
			mig = &migration{version: v, name: m[2]}
			byVersion[v] = mig
		}
		if mig.name != m[2] {
			t.Errorf("version %04d has two different names: %q and %q", v, mig.name, m[2])
		}
		if m[3] == "up" {
			mig.up = string(body)
		} else {
			mig.down = string(body)
		}
	}
	out := make([]migration, 0, len(byVersion))
	for _, m := range byVersion {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out
}

func TestMigrationsAreContiguousAndPaired(t *testing.T) {
	migs := load(t)
	if len(migs) == 0 {
		t.Fatal("no migrations found; the test is not checking anything")
	}
	for i, m := range migs {
		if m.version != i+1 {
			t.Errorf("versions must be contiguous from 0001: found %04d at position %d", m.version, i+1)
		}
		if strings.TrimSpace(m.up) == "" {
			t.Errorf("%04d_%s has no up migration", m.version, m.name)
		}
		if strings.TrimSpace(m.down) == "" {
			t.Errorf("%04d_%s has no down migration", m.version, m.name)
		}
	}
}

// destructive matches statements an up migration must never contain (expand/contract, ADR-015).
var destructive = regexp.MustCompile(`(?im)^\s*(DROP\s+(TABLE|COLUMN|INDEX|SCHEMA|CONSTRAINT|TRIGGER|VIEW|ROLE)|TRUNCATE|DELETE\s+FROM|ALTER\s+TABLE\s+\S+\s+(DROP|RENAME)|ALTER\s+TABLE\s+\S+\s+ALTER\s+COLUMN\s+\S+\s+(TYPE|SET\s+DATA\s+TYPE))\b`)

func TestUpMigrationsOnlyExpand(t *testing.T) {
	for _, m := range load(t) {
		if loc := destructive.FindString(m.up); loc != "" {
			t.Errorf("%04d_%s up migration contains a destructive statement: %q. Use expand/contract in a later release.",
				m.version, m.name, strings.TrimSpace(loc))
		}
	}
}

var (
	createTable = regexp.MustCompile(`(?im)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`)
	revokePub   = regexp.MustCompile(`(?im)^\s*REVOKE\s+ALL\s+ON\s+[^;]*\bFROM\s+PUBLIC\b`)
)

// TestEveryTableHasExplicitGrants keeps privileges fail-closed: a table created without GRANTs is unreachable.
func TestEveryTableHasExplicitGrants(t *testing.T) {
	for _, m := range load(t) {
		for _, c := range createTable.FindAllStringSubmatch(m.up, -1) {
			table := c[1]
			grant := regexp.MustCompile(`(?is)GRANT\s+[^;]*\bON\s+(?:[a-z_][a-z0-9_]*\s*,\s*)*` + table + `\b[^;]*\bTO\b`)
			if !grant.MatchString(m.up) {
				t.Errorf("%04d_%s creates table %s but never grants access to it", m.version, m.name, table)
			}
			if !revokePub.MatchString(m.up) || !regexp.MustCompile(`(?is)REVOKE\s+ALL\s+ON\s+[^;]*\b`+table+`\b`).MatchString(m.up) {
				t.Errorf("%04d_%s creates table %s but does not REVOKE ALL FROM PUBLIC on it", m.version, m.name, table)
			}
		}
	}
}

func TestAppendOnlyTablesHaveNoUpdateOrDeleteGrantsToTheApplication(t *testing.T) {
	appendOnly := []string{"audit_events"}
	for _, m := range load(t) {
		for _, table := range appendOnly {
			re := regexp.MustCompile(`(?is)GRANT\s+([^;]*?)\s+ON\s+[^;]*\b` + table + `\b[^;]*\bTO\s+fip_(app|admin)\b`)
			for _, g := range re.FindAllStringSubmatch(m.up, -1) {
				if regexp.MustCompile(`(?i)\b(UPDATE|DELETE|TRUNCATE|ALL)\b`).MatchString(g[1]) {
					t.Errorf("%04d_%s grants %q on append-only table %s to fip_%s", m.version, m.name, g[1], table, g[2])
				}
			}
		}
	}
}

func TestDownMigrationsAreCommentedAsDevelopmentOnly(t *testing.T) {
	for _, m := range load(t) {
		if !strings.Contains(strings.ToLower(m.down), "development only") {
			t.Errorf("%04d_%s down migration must state that it is for local development only", m.version, m.name)
		}
	}
}
