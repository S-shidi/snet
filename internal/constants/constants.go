// Package constants holds build-time invariants shared by the client daemon,
// control API and UI layers.
package constants

// DefaultServerAddr is the only coordination server this product talks to. It
// is hardcoded across all surfaces (daemon, ctl, mobile, web, desktop) so the
// server address is never user-configurable or displayed as plaintext.
const DefaultServerAddr = "https://snet.uizhi.eu.org:8090"