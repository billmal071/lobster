package cmd

import "testing"

func TestDownloadFlagAllowsNoArg(t *testing.T) {
	lookup := rootCmd.PersistentFlags().Lookup("download")
	if lookup == nil {
		t.Fatal("download flag lookup returned nil")
	}
	if lookup.NoOptDefVal != downloadFlagDefaultSentinel {
		t.Fatalf("download NoOptDefVal = %q, want %q", lookup.NoOptDefVal, downloadFlagDefaultSentinel)
	}
}
