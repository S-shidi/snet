//go:build android

package client

// hardwareID on Android returns empty. The Kotlin layer (HardwareID.kt)
// computes the hardware-bound device ID from Build.* + ANDROID_ID and passes
// it to Go via SnetCore.SetHardwareID(). Go should never generate its own
// hardware ID on Android.
func hardwareID() (string, error) {
	return "", nil
}
