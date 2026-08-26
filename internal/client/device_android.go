//go:build android

package client

// SetDeviceIDFile overrides the default device ID file path for Android,
// where /usr/local/snet/ does not exist. Must be called before
// LoadOrCreateDeviceID, typically from the Java/Kotlin layer at startup.
func SetDeviceIDFile(path string) {
	DefaultDeviceIDFile = path
}

// hardwareID on Android returns empty. The Kotlin layer (HardwareID.kt)
// computes the hardware-bound device ID from Build.* + ANDROID_ID and passes
// it to Go via SnetCore.SetHardwareID(). Go should never generate its own
// hardware ID on Android.
func hardwareID() (string, error) {
	return "", nil
}
