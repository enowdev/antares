package main

import (
	"os/exec"
	"strings"
)

// computerName is the name shown in System Settings > General > Sharing.
func computerName() string {
	out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
