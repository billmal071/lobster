package history

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"lobster/internal/media"
)

// dataHomeEnv names the environment variable config.dataDir consults on this
// platform, so a child process can be pointed at the same history file as the
// parent test.
func dataHomeEnv() string {
	if runtime.GOOS == "windows" {
		return "LOCALAPPDATA"
	}
	return "XDG_DATA_HOME"
}

// concurrentWriters is how many writers race for the history file. Enough that
// at least two overlap in practice; small enough to stay fast under -race.
const concurrentWriters = 8

// TestSaveFromSeparateProcessesKeepsEveryRow models the race that is actually
// reachable for this program: two `lobster play` runs alive at the same time,
// each calling Save on the one history file. Save loads the whole file,
// mutates one row and writes the result back through a temp file and a rename.
// The rename is atomic; the load-mutate-write around it is not. A writer that
// loaded before another's rename writes back a file that never contained the
// other's row, and that row is gone for good.
//
// This is deliberately a MULTI-PROCESS test, not goroutines: goroutines would
// be satisfied by a package mutex, which cannot serialise two lobster
// processes and so would not close this race at all. Each child does exactly
// one Save of its own distinct row and waits at a file barrier first, so the
// writes overlap rather than queueing up behind process startup.
func TestSaveFromSeparateProcessesKeepsEveryRow(t *testing.T) {
	if os.Getenv("LOBSTER_TEST_SAVE_ROW") != "" {
		t.Skip("running as the child writer")
	}

	dir := t.TempDir()
	barrier := filepath.Join(dir, "start")

	var wg sync.WaitGroup
	for i := 0; i < concurrentWriters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestChildWriterSavesOneRow$", "-test.v")
			cmd.Env = append(os.Environ(),
				dataHomeEnv()+"="+dir,
				"LOBSTER_TEST_SAVE_ROW="+strconv.Itoa(i),
				"LOBSTER_TEST_SAVE_BARRIER="+barrier,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("child %d: %v\n%s", i, err, out)
			}
		}(i)
	}

	// Let every child reach the barrier before releasing them together.
	time.Sleep(300 * time.Millisecond)
	if err := os.WriteFile(barrier, nil, 0600); err != nil {
		t.Fatalf("releasing barrier: %v", err)
	}
	wg.Wait()

	setDataHome(t, dir)
	entries, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != concurrentWriters {
		t.Fatalf("history holds %d rows, want %d: Save's load-mutate-rename is not atomic across processes, so a writer that loaded before another's rename drops that writer's row: %+v",
			len(entries), concurrentWriters, entries)
	}
}

// TestChildWriterSavesOneRow is the child half of
// TestSaveFromSeparateProcessesKeepsEveryRow. It is a no-op unless the parent
// invoked it with LOBSTER_TEST_SAVE_ROW set.
func TestChildWriterSavesOneRow(t *testing.T) {
	row := os.Getenv("LOBSTER_TEST_SAVE_ROW")
	if row == "" {
		t.Skip("helper for TestSaveFromSeparateProcessesKeepsEveryRow")
	}

	if barrier := os.Getenv("LOBSTER_TEST_SAVE_BARRIER"); barrier != "" {
		deadline := time.Now().Add(30 * time.Second)
		for {
			if _, err := os.Stat(barrier); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("barrier %s never appeared", barrier)
			}
			time.Sleep(time.Millisecond)
		}
	}

	if err := Save(media.HistoryEntry{
		ID:    "movie/" + row,
		Title: "Row " + row,
		Type:  media.Movie,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
}
