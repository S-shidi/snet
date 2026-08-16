//go:build windows

package client

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

func init() {
	DefaultDeviceIDFile = `C:\ProgramData\SNET\device.id`
}

// hardwareID returns the machine's stable hardware identifier (the Windows
// MachineGuid), which survives both app and OS reinstalls.
func hardwareID() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	guid, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return "", fmt.Errorf("MachineGuid: %w", err)
	}
	return guid, nil
}
