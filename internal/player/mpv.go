package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"lobster/internal/media"
)

// MPV implements the Player interface for mpv.
// Uses exec.Command with explicit args (no shell interpretation)
// and IPC via Unix socket at a randomized temp path.
type MPV struct {
	audioLang  string
	checkpoint func(position, duration float64)
}

func (m *MPV) Name() string { return "mpv" }

// SetCheckpoint installs a callback that receives the current playback
// position and duration periodically (throttled to checkpointInterval) while
// playback is running. Must be called before Play; the callback is never
// invoked after Play returns.
func (m *MPV) SetCheckpoint(fn func(position, duration float64)) { m.checkpoint = fn }

func (m *MPV) Available() bool {
	bin := mpvBinaryName()
	_, err := exec.LookPath(bin)
	if err != nil {
		// mpvBinaryName may return a full path on Windows
		_, err = os.Stat(bin)
	}
	return err == nil
}

// Play launches mpv with the given stream and returns the final playback state.
func (m *MPV) Play(stream *media.Stream, title string, startPos float64, subFiles []string) (PlayResult, error) {
	stream, cleanup, err := wrapDeobfuscated(stream)
	if err != nil {
		return PlayResult{}, err
	}
	defer cleanup()

	// Create randomized IPC path (Unix socket on Unix, named pipe on Windows)
	ipc, err := newIPCSocket()
	if err != nil {
		return PlayResult{}, err
	}
	defer ipc.cleanup()

	// Build args as explicit slice — each arg is separate, no shell interpretation
	args := []string{
		stream.URL,
		"--force-media-title=" + title,
		"--input-ipc-server=" + ipc.path,
		"--really-quiet",
		"--network-timeout=15",
	}

	args = append(args, audioLangArgs(m.audioLang)...)
	args = append(args, mpvHeaderArgs(stream)...)

	if startPos > 0 {
		args = append(args, fmt.Sprintf("--start=+%.0f", startPos))
	}

	if len(subFiles) > 0 {
		for _, sf := range subFiles {
			args = append(args, "--sub-file="+sf)
		}
	} else {
		// If we have embedded subtitles from the stream, add them
		for _, sub := range stream.Subtitles {
			if sub.URL != "" {
				args = append(args, "--sub-file="+sub.URL)
			}
		}
	}

	cmd := exec.Command(mpvBinaryName(), args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		return PlayResult{}, fmt.Errorf("starting mpv: %w", err)
	}

	// Track position and duration from IPC in a goroutine, using atomics to avoid data race.
	// The ipcDone channel is used to synchronize the goroutine before reading final values.
	var posBits, durBits atomic.Uint64
	var observed atomic.Bool
	ipcDone := make(chan struct{})
	procDone := make(chan struct{})
	startTime := time.Now()
	go func() {
		defer close(ipcDone)
		pos, dur, ok := m.trackPlayback(ipc, procDone)
		posBits.Store(math.Float64bits(pos))
		durBits.Store(math.Float64bits(dur))
		observed.Store(ok)
	}()

	waitErr := cmd.Wait()
	// mpv is gone; tell the collector so a still-unconnected dial loop gives
	// up now instead of running out its full retry bound, then wait for it to
	// finish so the final position/duration values are fully written before
	// we read them.
	close(procDone)
	<-ipcDone
	elapsed := time.Since(startTime)
	result := PlayResult{
		Position: math.Float64frombits(posBits.Load()),
		Duration: math.Float64frombits(durBits.Load()),
		// The tracker either observed a position for this session or it saw
		// nothing at all; in the second case Position is a default, not a
		// measurement, and callers must not persist it.
		PositionUnknown: !observed.Load(),
	}

	if waitErr != nil {
		// mpv returns non-zero on user quit (code 4), which is normal
		if exitErr, ok := waitErr.(*exec.ExitError); ok && exitErr.ExitCode() == 4 {
			return result, nil
		}
		return result, fmt.Errorf("mpv exited: %w", waitErr)
	}

	// If mpv exited almost instantly with no playback progress,
	// the stream likely failed to load (e.g., CDN unreachable).
	if result.Position == 0 && elapsed < 5*time.Second {
		return PlayResult{}, fmt.Errorf("stream failed to load (mpv exited in %s with no playback)", elapsed.Round(time.Millisecond))
	}

	return result, nil
}

// ipcDialTimeout bounds how long trackPlayback keeps retrying the IPC dial
// while mpv starts up; ipcDialInterval is the pause between attempts. Package
// vars so tests can shorten them.
var (
	ipcDialTimeout  = 30 * time.Second
	ipcDialInterval = 250 * time.Millisecond
)

// checkpointInterval throttles the periodic position checkpoints trackPlayback
// hands to the SetCheckpoint callback. A package var so tests can shorten it.
var checkpointInterval = 30 * time.Second

// dialWithRetry dials the mpv IPC socket until it succeeds, the retry bound
// elapses, or stop closes (mpv exited). mpv creates the socket only once it
// is up, which on a slow network stream can take well over any fixed grace
// period — a single unretried dial here used to record whole watches as
// position 0. Retrying is safe: a dead-on-arrival mpv closes stop within
// seconds, which ends the loop long before the bound.
func dialWithRetry(ipc *ipcSocket, stop <-chan struct{}) (io.ReadWriteCloser, error) {
	deadline := time.Now().Add(ipcDialTimeout)
	for {
		conn, err := ipc.dial()
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no connection within %s: %w", ipcDialTimeout, err)
		}
		select {
		case <-stop:
			return nil, fmt.Errorf("mpv exited before the socket appeared: %w", err)
		case <-time.After(ipcDialInterval):
		}
	}
}

// trackPlayback polls mpv's IPC for the current playback position and duration.
// The third return says whether mpv ever reported a position at all: false
// means the socket never came up, or came up and said nothing, so the returned
// 0 carries no information about where the watch stopped. Callers need that
// distinction to avoid writing 0 over a real resume point.
// If a checkpoint callback is installed, it receives (position, duration)
// periodically — throttled to checkpointInterval — so a hard shutdown that
// kills mpv and this process at once loses at most one interval of position.
// The callback runs on its own goroutine (a slow history write must not back
// up the IPC event loop), but trackPlayback drains it before returning, so no
// invocation can interleave with the caller's exit-time save.
func (m *MPV) trackPlayback(ipc *ipcSocket, stop <-chan struct{}) (float64, float64, bool) {
	var lastPos, lastDur float64
	var observed bool

	var ckCh chan [2]float64
	ckDone := make(chan struct{})
	if m.checkpoint != nil {
		ckCh = make(chan [2]float64, 1)
		go func() {
			defer close(ckDone)
			for v := range ckCh {
				m.checkpoint(v[0], v[1])
			}
		}()
	} else {
		close(ckDone)
	}
	defer func() {
		if ckCh != nil {
			close(ckCh)
		}
		<-ckDone
	}()

	conn, err := dialWithRetry(ipc, stop)
	if err != nil {
		// Playback proceeds without tracking; say so instead of silently
		// recording the watch as position 0.
		fmt.Fprintf(os.Stderr, "mpv ipc: position tracking unavailable: %v\n", err)
		return 0, 0, false
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)

	// Observe time-pos and duration properties
	for i, prop := range []string{"time-pos", "duration"} {
		cmd := map[string]interface{}{
			"command":    []interface{}{"observe_property", i + 1, prop},
			"request_id": 100 + i,
		}
		data, _ := json.Marshal(cmd)
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			// IPC write failed — mpv may have exited early or the socket
			// is broken. Return zero state so the caller can detect this.
			fmt.Fprintf(os.Stderr, "mpv ipc: failed to observe %s: %v\n", prop, err)
			return 0, 0, false
		}
	}

	lastCheckpoint := time.Now()
	for scanner.Scan() {
		line := scanner.Text()
		// Data is a pointer because mpv sends "data":null for a property it
		// has no value for yet — before playback starts, and again as it shuts
		// down. A null must not count as an observation, and a plain float64
		// would decode it as an indistinguishable 0.
		var event struct {
			Event string   `json:"event"`
			Name  string   `json:"name"`
			Data  *float64 `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Name == "time-pos" && event.Data != nil {
			// mpv told us where playback is, so the position this call returns
			// is a measurement — including when that measurement is 0, which
			// is why this is set before the > 0 filter below rather than from
			// lastPos.
			observed = true
		}
		if event.Name == "time-pos" && event.Data != nil && *event.Data > 0 {
			lastPos = *event.Data
			// Hand the position to the checkpoint writer, at most once per
			// interval. The send never blocks: if the writer is still busy
			// with the previous write, this position is simply skipped and a
			// later event delivers a fresher one.
			if ckCh != nil && time.Since(lastCheckpoint) >= checkpointInterval {
				select {
				case ckCh <- [2]float64{lastPos, lastDur}:
					lastCheckpoint = time.Now()
				default:
				}
			}
		}
		if event.Name == "duration" && event.Data != nil && *event.Data > 0 {
			lastDur = *event.Data
		}
	}

	return lastPos, lastDur, observed
}

// formatDuration formats seconds as HH:MM:SS.
func formatDuration(seconds float64) string {
	s := int(seconds)
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

// parseDuration parses HH:MM:SS or MM:SS into seconds.
func parseDuration(s string) float64 {
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 3:
		h, _ := strconv.ParseFloat(parts[0], 64)
		m, _ := strconv.ParseFloat(parts[1], 64)
		sec, _ := strconv.ParseFloat(parts[2], 64)
		return h*3600 + m*60 + sec
	case 2:
		m, _ := strconv.ParseFloat(parts[0], 64)
		sec, _ := strconv.ParseFloat(parts[1], 64)
		return m*60 + sec
	default:
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
}
