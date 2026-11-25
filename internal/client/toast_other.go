//go:build !windows

package client

import "log"

// RegisterAppID does nothing outside Windows.
func RegisterAppID(string) error { return nil }

// ShowToast logs the message outside Windows.
func ShowToast(title, body string) error {
	log.Printf("notification: %s - %s", title, body)
	return nil
}
