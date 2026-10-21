package scanner

import (
	"regexp"

	"strconv"

	"strings"
)

type semanticVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []string
}

var semanticVersionPattern = regexp.MustCompile(`^[vV]?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

func parseSemanticVersion(value string) (semanticVersion, bool) {
	matches := semanticVersionPattern.FindStringSubmatch(value)
	if len(matches) != 5 {
		return semanticVersion{}, false
	}
	for _, part := range matches[1:4] {
		if len(part) > 1 && part[0] == '0' {
			return semanticVersion{}, false
		}
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return semanticVersion{}, false
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return semanticVersion{}, false
	}
	patch, err := strconv.Atoi(matches[3])
	if err != nil {
		return semanticVersion{}, false
	}
	var prerelease []string
	if matches[4] != "" {
		prerelease = strings.Split(matches[4], ".")
		for _, identifier := range prerelease {
			if identifier == "" {
				return semanticVersion{}, false
			}
			if _, err := strconv.Atoi(identifier); err == nil && len(identifier) > 1 && identifier[0] == '0' {
				return semanticVersion{}, false
			}
		}
	}
	return semanticVersion{major: major, minor: minor, patch: patch, prerelease: prerelease}, true
}

func compareSemanticVersions(a, b semanticVersion) int {
	if a.major != b.major {
		if a.major > b.major {
			return 1
		}
		return -1
	}
	if a.minor != b.minor {
		if a.minor > b.minor {
			return 1
		}
		return -1
	}
	if a.patch != b.patch {
		if a.patch > b.patch {
			return 1
		}
		return -1
	}
	if len(a.prerelease) == 0 && len(b.prerelease) == 0 {
		return 0
	}
	if len(a.prerelease) == 0 {
		return 1
	}
	if len(b.prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(a.prerelease) && i < len(b.prerelease); i++ {
		left, right := a.prerelease[i], b.prerelease[i]
		leftNumber, leftErr := strconv.Atoi(left)
		rightNumber, rightErr := strconv.Atoi(right)
		switch {
		case leftErr == nil && rightErr == nil && leftNumber != rightNumber:
			if leftNumber > rightNumber {
				return 1
			}
			return -1
		case leftErr == nil && rightErr != nil:
			return -1
		case leftErr != nil && rightErr == nil:
			return 1
		case left != right:
			if left > right {
				return 1
			}
			return -1
		}
	}
	if len(a.prerelease) > len(b.prerelease) {
		return 1
	}
	if len(a.prerelease) < len(b.prerelease) {
		return -1
	}
	return 0
}
