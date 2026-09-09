// Package cmd implements the CLI commands using Cobra.
package cmd

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/config"
	"lobster/internal/history"
	"lobster/internal/torrentstream"
)

// Version is set at build time via ldflags.
var Version = "dev"

// Global flags
var (
	flagDownload  string
	flagLanguage  string
	flagAudioLang string
	flagNoSubs    bool
	flagProvider  string
	flagQuality   string
	flagPlayer    string
	flagBase      string
	flagContinue  bool
	flagJSON      bool
	flagDebug     bool
)

// cfg holds the loaded configuration (merged: defaults < config file < flags).
var cfg *config.Config

var rootCmd = &cobra.Command{
	Use:   "lobster [query]",
	Short: "Stream movies and TV shows from the terminal",
	Long: `Lobster is a security-hardened terminal media streamer.
Search for movies and TV shows, stream them with mpv/vlc, or download with ffmpeg.`,
	Args:              cobra.ArbitraryArgs,
	PersistentPreRunE: loadConfig,
	RunE:              searchRun,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// Agent commands have already written a JSON error envelope to stdout
		// and carry their own exit code; printing again would corrupt it.
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		os.Exit(1)
	}
}

// registerPersistentFlags binds every global flag as a persistent flag of c.
// It is split out of init so a test can bind the same set onto a throwaway
// command and parse a real argv through it, which is the only way to observe
// the values the CLI actually produces for an invocation: pflag applies a
// flag's default at registration time, so re-parsing rootCmd's own FlagSet
// would only report whatever the globals already hold.
func registerPersistentFlags(c *cobra.Command) {
	fs := c.PersistentFlags()
	fs.StringVarP(&flagDownload, "download", "d", "", "Download to path instead of playing (default: config download_dir)")
	fs.StringVarP(&flagLanguage, "language", "l", "", "Subtitle language (default: english)")
	fs.StringVarP(&flagAudioLang, "audio-language", "a", "", "Preferred audio track language (default: english)")
	fs.BoolVarP(&flagNoSubs, "no-subs", "n", false, "Disable subtitles")
	fs.StringVarP(&flagProvider, "provider", "p", "", "Server provider: Vidcloud | UpCloud")
	fs.StringVarP(&flagQuality, "quality", "q", "", "Video quality: 360 | 480 | 720 | 1080 | best")
	fs.StringVar(&flagPlayer, "player", "", "Media player: mpv | vlc | iina | celluloid")
	// Resuming a part-watched title is the default on every playback entry
	// point, so it lives here rather than being re-opted-into per command:
	// the interactive search path and the TV/session path both read
	// flagContinue directly and had no opt-in of their own, which is how
	// `lobster "<title>"` came to restart films the agent `play` command
	// resumed. --continue=false remains a deliberate fresh start, and is
	// forwarded verbatim to a detached child by detach.go's Changed()
	// handling, while an unpassed flag is not forwarded at all and the
	// child re-applies this default itself.
	fs.BoolVarP(&flagContinue, "continue", "c", true, "Auto-resume from history (--continue=false to start fresh)")
	fs.BoolVarP(&flagJSON, "json", "j", false, "Output stream metadata as JSON")
	// Kept to one line and in step with GUIDE.md's "Content sources" table,
	// which is where each value's scope and limits are written down; an
	// unrecognised value is not rejected but falls through to moviebox
	// (newProvider, cmd/provider.go), which is worth knowing before typing one.
	fs.StringVar(&flagBase, "base", "", "Content source: auto | soap2day | vaplayer | flixhq.to | flixhq.ws | tbcpl | 1shows.org | kimcartoon | allanime | moviebox | vidnest | yts (see GUIDE.md)")
	fs.BoolVarP(&flagDebug, "debug", "x", false, "Debug logging to stderr")
}

func init() {
	registerPersistentFlags(rootCmd)

	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(findCmd)
	rootCmd.AddCommand(episodesCmd)
	rootCmd.AddCommand(channelsCmd)
	rootCmd.AddCommand(playCmd)
	rootCmd.AddCommand(historyCmd)
	rootCmd.AddCommand(trendingCmd)
	rootCmd.AddCommand(recentCmd)
	rootCmd.AddCommand(versionCmd)

	// The commands that end in resolveAndPlay: the bare root (searchRun's
	// interactive picker), play (agentResolveAndPlay), history (historyRun),
	// and the two browse commands (trendingRun/recentRun).
	markPlaybackCommand(rootCmd)
	markPlaybackCommand(playCmd)
	markPlaybackCommand(historyCmd)
	markPlaybackCommand(trendingCmd)
	markPlaybackCommand(recentCmd)
}

// loadConfig is the root's PersistentPreRunE, so it runs for the interactive
// commands and the agent commands alike. The two need different failure
// reporting: an interactive user gets cobra's "Error: ..." on stderr, while an
// agent command must produce the JSON envelope its caller parses
// unconditionally — and, because SilenceErrors is set there, a bare error
// would otherwise vanish entirely.
func loadConfig(cmd *cobra.Command, args []string) error {
	if err := applyConfig(); err != nil {
		if isAgentCommand(cmd) {
			return emitErr("config_invalid", exitUsage, "%v", err)
		}
		return err
	}

	// The torrent library picks its storage backend in its own init(), so the
	// only way onto the one that cannot SIGBUS is to start the process again
	// with the variable already set. Do it here, once the effective base and
	// fallback setting are known but before any search, network call or
	// terminal setup — the re-exec replays argv, so anything the user would
	// not want repeated must not have happened yet.
	//
	// Gated on the command, not on the output flags. This is
	// PersistentPreRunE, so it runs for `version`, `doctor`, `find`,
	// `episodes` and `channels` too, and none of them can reach a magnet: the
	// per-type route to YTS runs from resolveAndPlay only. Flag-gating was
	// tried and does not cover them — `version` passes neither --json nor
	// --download, so it re-execed all the same, and on Windows (canExec
	// false) planFileIo warns instead, printing a SIGBUS notice ahead of the
	// version string. The flag conditions inside mayStreamTorrent still earn
	// their place for the commands that do play; they are just not sufficient
	// on their own.
	if reachesPlayback(cmd) {
		ensureSafeStorage(mayStreamTorrent(cfg), warnf)
	}
	return nil
}

// playbackCommands is the set of commands that can reach resolveAndPlay, and
// so the set for which the torrent storage backend matters. Membership is
// declared at registration (init) rather than inferred, because there is no
// way to ask a cobra command what its RunE eventually calls.
var playbackCommands = map[*cobra.Command]bool{}

// markPlaybackCommand records that c can reach resolveAndPlay.
func markPlaybackCommand(c *cobra.Command) { playbackCommands[c] = true }

// reachesPlayback reports whether cmd can end in playback. A command absent
// from the set is treated as one that cannot, which is the safe direction for
// the wrong answer: the cost is a run that streams a torrent on the
// memory-mapped backend only if a playback command is ever added without being
// marked — and TestOnlyCommandsThatCanPlayChooseTheStorageBackend fails on any
// unlisted command to stop exactly that.
func reachesPlayback(cmd *cobra.Command) bool { return cmd != nil && playbackCommands[cmd] }

// ensureSafeStorage is torrentstream.EnsureSafeStorage behind a package var.
// The mechanism's own no-op path is environment-driven
// (TORRENT_STORAGE_DEFAULT_FILE_IO, which this package's TestMain presets so a
// test binary can never re-exec itself), so a test asserting on the environment
// could not see whether the call happens at all. This seam can.
var ensureSafeStorage = torrentstream.EnsureSafeStorage

// applyConfig loads and merges configuration: defaults < config file < CLI flags.
func applyConfig() error {
	var err error
	cfg, err = config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// CLI flags override config file values
	if flagPlayer != "" {
		cfg.Player = flagPlayer
	}
	if flagProvider != "" {
		cfg.Provider = flagProvider
	}
	if flagQuality != "" {
		cfg.Quality = flagQuality
	}
	if flagAudioLang != "" {
		cfg.AudioLanguage = flagAudioLang
	}
	if flagLanguage != "" {
		cfg.SubsLanguage = flagLanguage
	}
	if flagBase != "" {
		cfg.Base = flagBase
	}
	if flagDebug {
		cfg.Debug = true
	}

	// Re-validate after flag overrides
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	if cfg.Debug {
		log.SetOutput(os.Stderr)
		log.SetPrefix("[lobster] ")
	} else {
		log.SetOutput(os.Stderr)
		log.SetFlags(0)
	}

	// The history package falls back to an unlocked write when its lock cannot
	// be established. Route that notice through debugf so the downgrade is
	// diagnosable instead of silent.
	history.SetLogger(debugf)

	return nil
}

// mayStreamTorrent reports whether this run could open a magnet, which decides
// whether the storage backend matters at all.
//
// YTS is the only provider that resolves to one. Three ways of reaching it are
// visible to this function, because all three are settled by the time it is
// called: named as the base, enabled as a fallback, or reached by the per-type
// route, which sends every movie to YTS whenever the user has named no source
// of their own (routeByType and baseIsAuto, cmd/typeroute.go). Since that is
// the *default* configuration, the third way is the common one — leaving it
// out put the default install on
// the memory-mapped backend, which is the SIGBUS this whole mechanism exists
// to avoid. Anything else resolves to HTTP or HLS and never reaches the
// torrent client.
//
// There is a fourth way, and it is deliberately not answered here: a ref
// carries the base it was found under, and applyRefBase (cmd/play.go) copies
// that into cfg.Base inside RunE — after this function has been consulted and
// the backend decided. `lobster play --ref <ref minted under --base yts>`
// under a configured `base = "soap2day"` therefore streams a magnet on
// whichever backend a non-torrent run was given. It cannot be closed from
// here: the value is not knowable until the ref is decoded, and re-execing
// once it is would replay argv after the process has committed to the
// invocation. applyRefBase warns instead
// (torrentstream.WarnLateStorageRisk).
//
// It answers for the run, not for a particular selection, because it is
// consulted before any search: under auto it cannot yet know whether the user
// will pick a movie (routed to YTS) or a series (never routed there). The
// conservative answer is the safe one — the cost of a false positive is one
// re-exec, and of a false negative a process killed mid-playback.
func mayStreamTorrent(c *config.Config) bool {
	if c == nil {
		return false
	}
	if strings.EqualFold(c.Base, "yts") || c.TorrentFallback {
		return true
	}
	// The auto arm mirrors routeByType's own conditions (cmd/typeroute.go),
	// because under auto the route is the only thing that reaches YTS:
	//
	//   - a configured api_url overrides Base entirely (baseIsAuto), so no
	//     movie is routed and nothing in the run can reach a magnet;
	//   - --json and --download return before the lookup, since a magnet is
	//     neither a URL a JSON consumer can open nor something the download
	//     path accepts.
	//
	// Cobra parses flags before PersistentPreRunE, so both are populated by
	// the time loadConfig calls this. They are worth reading because a "yes"
	// is not free: it re-execs the process, or on Windows (canExec false)
	// prints an ungated SIGBUS notice. Which subcommand is running is handled
	// separately and earlier, by reachesPlayback — this function is only ever
	// asked about a command that can play.
	if flagJSON || flagDownload != "" {
		return false
	}
	return c.APIURL == "" && strings.EqualFold(c.Base, config.BaseAuto)
}

// warnf reports something the user should know but that does not stop the run.
// Unlike debugf it is not gated on the debug flag: a silent downgrade to a
// backend that can kill the process is exactly the kind of thing that should
// not need a flag to be seen.
// A package var, not a plain func, so a test can observe what was warned about
// without capturing os.Stderr.
var warnf = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "lobster: "+format+"\n", args...)
}

func debugf(format string, args ...interface{}) {
	if cfg != nil && cfg.Debug {
		log.Printf(format, args...)
	}
}
