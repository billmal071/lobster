package cmd

import "testing"

func TestDownloadFlagExists(t *testing.T) {
	lookup := rootCmd.PersistentFlags().Lookup("download")
	if lookup == nil {
		t.Fatal("download flag lookup returned nil")
	}
}
