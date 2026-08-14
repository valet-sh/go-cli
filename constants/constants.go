package constants

const (
	VshStableVersion = "3.x"
	VshNextVersion   = "next"

	Vsh3xMinMacOSVersion = "26.0"
	Vsh3xMinLinuxVersion = "24.04"

	VshRootPath = "/usr/local/valet-sh"

	VshBasePath = VshRootPath + "/valet-sh"
	VshVenvPath = VshRootPath + "/venv"
	VshEtcPath  = VshRootPath + "/etc"

	VshAnsibleFactsFile = "/tmp/ansible-facts/local"
	VshServiceFile      = VshEtcPath + "/services.yml"
	VshBundlesFile      = VshEtcPath + "/bundles.yml"

	VshReleaseChannelFile     = "RELEASE_CHANNEL"
	VshReleaseChannelFilePath = VshEtcPath + "/" + VshReleaseChannelFile

	VshCliRepo       = "valet-sh/go-cli"
	VshPlaybookRepo  = "valet-sh/valet-sh"
	VshCliReleaseURL = "https://api.github.com/repos/" + VshCliRepo + "/releases/latest"

	VshRuntimeRepo        = "valet-sh/runtime"
	VshRuntimeInstallBase = VshRootPath
	VshRuntimeVersionFile = VshVenvPath + "/.version"
)
