//go:build !windows

package main

func hasGUI() bool         { return false }
func runGUI()              { runHeadless() }
func setAutoStart(bool)    {}
func openConfigInEditor()  {}
