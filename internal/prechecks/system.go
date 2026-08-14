package prechecks

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/valet-sh/cli/constants"
)

type CheckResult struct {
	Passed  bool
	Message string
	Details string
}

func RequirementsCheck() error {
	checks := []CheckResult{
		CheckOSVersionRequirements(),
		CheckSystemArchitecture(),
	}

	var failures []string
	for _, result := range checks {
		if !result.Passed {
			failures = append(failures, fmt.Sprintf("%s\n  %s", result.Message, result.Details))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "\n"))
	}

	return nil
}

func CheckOSVersionRequirements() CheckResult {
	osInfo, err := host.Info()
	if err != nil {
		return CheckResult{
			Passed:  false,
			Message: "Failed to retrieve OS information",
			Details: err.Error(),
		}
	}

	currentVersion, err := version.NewVersion(osInfo.PlatformVersion)
	if err != nil {
		return CheckResult{
			Passed:  false,
			Message: "Failed to parse current OS version",
			Details: fmt.Sprintf("Version: %s, Error: %v", osInfo.PlatformVersion, err),
		}
	}

	minVersion, minVersionStr := getMinOSVersion(runtime.GOOS)
	if minVersion == nil {
		return CheckResult{
			Passed:  false,
			Message: "Unsupported operating system",
			Details: fmt.Sprintf("OS: %s. Only macOS and Linux are supported", runtime.GOOS),
		}
	}

	if !currentVersion.GreaterThanOrEqual(minVersion) {
		return CheckResult{
			Passed:  false,
			Message: "Unsupported OS version",
			Details: fmt.Sprintf("Current: %s, Required: %s or higher", currentVersion, minVersionStr),
		}
	}

	return CheckResult{Passed: true}
}

func CheckSystemArchitecture() CheckResult {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	supportedArch, err := getSupportedArchitecture(osName)
	if err != nil {
		return CheckResult{
			Passed:  false,
			Message: "Unsupported operating system",
			Details: err.Error(),
		}
	}

	if arch != supportedArch {
		return CheckResult{
			Passed:  false,
			Message: "Unsupported architecture",
			Details: fmt.Sprintf("Current: %s, Required: %s on %s", arch, supportedArch, osName),
		}
	}

	return CheckResult{Passed: true}
}

func getMinOSVersion(osName string) (*version.Version, string) {
	var minVersionStr string

	switch osName {
	case "darwin":
		minVersionStr = constants.Vsh3xMinMacOSVersion
	case "linux":
		minVersionStr = constants.Vsh3xMinLinuxVersion
	default:
		return nil, ""
	}

	minVersion, err := version.NewVersion(minVersionStr)
	if err != nil {
		return nil, minVersionStr
	}

	return minVersion, minVersionStr
}

func getSupportedArchitecture(osName string) (string, error) {
	switch osName {
	case "darwin":
		return "arm64", nil
	case "linux":
		return "amd64", nil
	default:
		return "", fmt.Errorf("unsupported operating system: %s", osName)
	}
}
