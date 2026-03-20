package version

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func Info() map[string]string {
	return map[string]string{
		"version":    Version,
		"commit":     Commit,
		"build_date": BuildDate,
	}
}
