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
	fs.StringVar(&flagBase, "base", "", "Content source: flixhq.to | flixhq.ws | kimcartoon.com.co | soap2day | moviebox | vaplayer | vidnest | tbcpl | 1shows.org | allanime | yts")
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
	return nil
}

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

	// The torrent library picks its storage backend in its own init(), so the
	// only way onto the one that cannot SIGBUS is to start the process again
	// with the variable already set. Do it here, once the effective base and
	// fallback setting are known but before any search, network call or
	// terminal setup — the re-exec replays argv, so anything the user would
	// not want repeated must not have happened yet.
	torrentstream.EnsureSafeStorage(mayStreamTorrent(cfg), warnf)

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

// debugf logs a message if debug mode is enabled.
// mayStreamTorrent reports whether this run could open a magnet, which decides
// whether the storage backend matters at all.
//
// YTS is the only provider that resolves to one, and it is reachable exactly
// two ways: named as the base, or enabled as a fallback. Anything else resolves
// to HTTP or HLS and never reaches the torrent client.
func mayStreamTorrent(c *config.Config) bool {
	if c == nil {
		return false
	}
	return strings.EqualFold(c.Base, "yts") || c.TorrentFallback
}

// warnf reports something the user should know but that does not stop the run.
// Unlike debugf it is not gated on the debug flag: a silent downgrade to a
// backend that can kill the process is exactly the kind of thing that should
// not need a flag to be seen.
func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "lobster: "+format+"\n", args...)
}

func debugf(format string, args ...interface{}) {
	if cfg != nil && cfg.Debug {
		log.Printf(format, args...)
	}
}
