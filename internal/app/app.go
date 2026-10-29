// Package app holds the application's names.
package app

const (
	// Name is the human-readable name.
	Name = "Buttonsmith"
	// ID names the command, config directory, systemd unit and udev rule.
	ID = "buttonsmith"
	// AppID names the desktop file and icon, and is the Wayland app ID, so
	// Plasma can match the settings window to its icon. Reverse-DNS without
	// dashes: icon lookup strips "-suffixes" and searches the current theme
	// first, so a dashed name could resolve to an unrelated theme icon.
	AppID = "io.github.amritanshu_pandey.Buttonsmith"
)
