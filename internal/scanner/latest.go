package scanner

import (
	"path/filepath"
	"regexp"
	"strings"
)

var javaArchiveVersionPattern = regexp.MustCompile(`^(.+)[-_]([vV]?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)

type artifactVersionCandidate struct {
	index   int
	group   string
	version semanticVersion
}

func LatestArtifacts(results []Result) []Result {
	latest := make(map[string]semanticVersion)
	var candidates []artifactVersionCandidate
	hasVersionCandidate := make([]bool, len(results))
	dockerTags := make([][]dockerVersionTag, len(results))
	versionedDockerRepositories := make(map[string]struct{})
	latestHelmResult := make(map[string]int)
	versionedHelmGroups := make(map[string]struct{})
	unversionedHelmResult := make(map[string]int)

	addCandidate := func(index int, group, tag string) bool {
		version, ok := parseSemanticVersion(tag)
		if !ok || group == "" {
			return false
		}
		candidates = append(candidates, artifactVersionCandidate{
			index: index, group: group, version: version,
		})
		hasVersionCandidate[index] = true
		if current, exists := latest[group]; !exists || compareSemanticVersions(version, current) > 0 {
			latest[group] = version
		}
		return true
	}

	for i, result := range results {
		switch result.Type {
		case "docker-image":
			if result.Inventory.Image == nil || len(result.Inventory.Image.Tags) == 0 {
				continue
			}
			for _, tag := range result.Inventory.Image.Tags {
				repository, versionTag, ok := parseDockerTag(tag)
				if !ok {
					dockerTags[i] = append(dockerTags[i], dockerVersionTag{tag: tag})
					continue
				}
				group := "docker:" + repository
				version, versioned := parseSemanticVersion(versionTag)
				if versioned {
					versionedDockerRepositories[group] = struct{}{}
					addCandidate(i, group, versionTag)
				}
				dockerTags[i] = append(dockerTags[i], dockerVersionTag{
					tag: tag, group: group, version: version, versioned: versioned,
				})
			}
		case "helm-chart":
			for _, chart := range helmChartVersions(result.Inventory) {
				group := "helm:" + strings.ToLower(chart.name)
				normalizedVersion, ok := normalizeHelmVersion(chart.version)
				if !ok {
					winner, hasWinner := unversionedHelmResult[group]
					if !hasWinner || results[i].Path < results[winner].Path {
						unversionedHelmResult[group] = i
					}
					continue
				}
				version, _ := parseSemanticVersion(normalizedVersion)
				versionedHelmGroups[group] = struct{}{}
				current, exists := latest[group]
				addCandidate(i, group, normalizedVersion)
				switch {
				case !exists, compareSemanticVersions(version, current) > 0:
					latestHelmResult[group] = i
				case compareSemanticVersions(version, current) == 0:
					winner, hasWinner := latestHelmResult[group]
					if !hasWinner || results[i].Path < results[winner].Path {
						latestHelmResult[group] = i
					}
				}
			}
		case "java-application":
			if result.latestKey != "" {
				addCandidate(i, result.latestKey, result.latestTag)
			}
		}
	}

	selected := make([]bool, len(results))
	for _, candidate := range candidates {
		if compareSemanticVersions(candidate.version, latest[candidate.group]) == 0 {
			selected[candidate.index] = true
		}
	}

	for i, result := range results {
		switch result.Type {
		case "docker-image":
			if result.Inventory.Image == nil || len(result.Inventory.Image.Tags) == 0 {
				selected[i] = true
				break
			}
			keptTags := make([]string, 0, len(dockerTags[i]))
			hasVersionedTag := false
			for _, tag := range dockerTags[i] {
				if tag.versioned {
					hasVersionedTag = true
					if compareSemanticVersions(tag.version, latest[tag.group]) == 0 {
						keptTags = append(keptTags, tag.tag)
					}
					continue
				}
				if _, hasVersionedRepository := versionedDockerRepositories[tag.group]; !hasVersionedRepository {
					keptTags = append(keptTags, tag.tag)
					continue
				}
			}
			if len(keptTags) > 0 && (!hasVersionedTag || selected[i]) {
				selected[i] = true
				result.Inventory.Image.Tags = sortedUnique(keptTags)
			} else {
				selected[i] = false
			}
		case "helm-chart":
			selected[i] = false
		case "java-application":
			if !hasVersionCandidate[i] {
				selected[i] = true
			}
		default:
			selected[i] = true
		}
	}
	for _, index := range latestHelmResult {
		selected[index] = true
	}
	for group, index := range unversionedHelmResult {
		if _, hasComparableVersion := versionedHelmGroups[group]; !hasComparableVersion {
			selected[index] = true
		}
	}

	filtered := make([]Result, 0, len(results))
	for i, result := range results {
		if selected[i] {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

type dockerVersionTag struct {
	tag       string
	group     string
	version   semanticVersion
	versioned bool
}

func parseJavaArchiveName(fileName string) (string, string, bool) {
	name := filepath.Base(fileName)
	lowerName := strings.ToLower(name)
	for _, extension := range []string{".tar.gz", ".tgz", ".tar"} {
		if strings.HasSuffix(lowerName, extension) {
			name = name[:len(name)-len(extension)]
			break
		}
	}
	matches := javaArchiveVersionPattern.FindStringSubmatch(name)
	if len(matches) != 3 || matches[1] == "" {
		return "", "", false
	}
	if _, ok := parseSemanticVersion(matches[2]); !ok {
		return "", "", false
	}
	return strings.TrimRight(matches[1], "-_"), matches[2], true
}

func parseDockerTag(reference string) (string, string, bool) {
	if strings.Contains(reference, "@") {
		return "", "", false
	}
	colon := strings.LastIndexByte(reference, ':')
	slash := strings.LastIndexByte(reference, '/')
	if colon <= slash || colon == len(reference)-1 {
		return "", "", false
	}
	repository, tag := reference[:colon], reference[colon+1:]
	if repository == "" {
		return "", "", false
	}
	return strings.ToLower(repository), tag, true
}
