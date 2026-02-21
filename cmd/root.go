// Package cmd implements the CLI commands using Cobra.
package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"lobster/internal/config"
)

// Version is set at build time via ldflags.
var Version = "dev"

// Global flags
var (
	flagDownload string
	flagLanguage string
	flagNoSubs   bool
	flagProvider string
	flagQuality  string
	flagPlayer   string
	flagContinue bool
	flagJSON     bool
	flagDebug    bool
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
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagDownload, "download", "d", "", "Download to path instead of playing")
	rootCmd.PersistentFlags().StringVarP(&flagLanguage, "language", "l", "", "Subtitle language (default: english)")
	rootCmd.PersistentFlags().BoolVarP(&flagNoSubs, "no-subs", "n", false, "Disable subtitles")
	rootCmd.PersistentFlags().StringVarP(&flagProvider, "provider", "p", "", "Server provider: Vidcloud | UpCloud")
	rootCmd.PersistentFlags().StringVarP(&flagQuality, "quality", "q", "", "Video quality: 360 | 480 | 720 | 1080")
	rootCmd.PersistentFlags().StringVar(&flagPlayer, "player", "", "Media player: mpv | vlc | iina | celluloid")
	rootCmd.PersistentFlags().BoolVarP(&flagContinue, "continue", "c", false, "Auto-resume from history")
	rootCmd.PersistentFlags().BoolVarP(&flagJSON, "json", "j", false, "Output stream metadata as JSON")
	rootCmd.PersistentFlags().BoolVarP(&flagDebug, "debug", "x", false, "Debug logging to stderr")

	rootCmd.AddCommand(historyCmd)
	rootCmd.AddCommand(trendingCmd)
	rootCmd.AddCommand(recentCmd)
	rootCmd.AddCommand(versionCmd)
}

// loadConfig loads and merges configuration: defaults < config file < CLI flags.
func loadConfig(cmd *cobra.Command, args []string) error {
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
	if flagLanguage != "" {
		cfg.SubsLanguage = flagLanguage
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

	return nil
}

// debugf logs a message if debug mode is enabled.
func debugf(format string, args ...interface{}) {
	if cfg != nil && cfg.Debug {
		log.Printf(format, args...)
	}
}
