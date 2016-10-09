package builder

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-tools/go-xamarin/constants"
	"github.com/bitrise-tools/go-xamarin/solution"
	"github.com/bitrise-tools/go-xamarin/utility"
)

func validateSolutionPth(pth string) error {
	ext := filepath.Ext(pth)
	if ext != constants.SolutionExt {
		return fmt.Errorf("path is not a solution file path: %s", pth)
	}
	if exist, err := pathutil.IsPathExists(pth); err != nil {
		return err
	} else if !exist {
		return fmt.Errorf("solution not exist at: %s", pth)
	}
	return nil
}

func validateSolutionConfig(solution solution.Model, configuration, platform string) error {
	config := utility.ToConfig(configuration, platform)
	if _, ok := solution.ConfigMap[config]; !ok {
		return fmt.Errorf("invalid solution config, available: %v", solution.ConfigList())
	}
	return nil
}

func isProjectTypeAllowed(projectType constants.ProjectType, projectTypeWhiteList ...constants.ProjectType) bool {
	if len(projectTypeWhiteList) == 0 {
		return true
	}

	for _, filter := range projectTypeWhiteList {
		switch filter {
		case constants.ProjectTypeIOS:
			if projectType == constants.ProjectTypeIOS {
				return true
			}
		case constants.ProjectTypeTvOS:
			if projectType == constants.ProjectTypeTvOS {
				return true
			}
		case constants.ProjectTypeMacOS:
			if projectType == constants.ProjectTypeMacOS {
				return true
			}
		case constants.ProjectTypeAndroid:
			if projectType == constants.ProjectTypeAndroid {
				return true
			}
		}
	}

	return false
}

func isArchitectureArchiveable(architectures ...string) bool {
	// default is armv7
	if len(architectures) == 0 {
		return true
	}

	for _, arch := range architectures {
		arch = strings.ToLower(arch)
		if !strings.HasPrefix(arch, "arm") {
			return false
		}
	}

	return true
}

func isPlatformAnyCPU(platform string) bool {
	return (platform == "Any CPU" || platform == "AnyCPU")
}

func androidPackageName(manifestPth string) (string, error) {
	content, err := fileutil.ReadStringFromFile(manifestPth)
	if err != nil {
		return "", err
	}

	return androidPackageNameFromManifestContent(content)
}

func androidPackageNameFromManifestContent(manifestContent string) (string, error) {
	// package is attribute of the rott xml element
	manifestContent = "<a>" + manifestContent + "</a>"

	type Manifest struct {
		Package string `xml:"package,attr"`
	}

	type Result struct {
		Manifest Manifest `xml:"manifest"`
	}

	var result Result
	if err := xml.Unmarshal([]byte(manifestContent), &result); err != nil {
		return "", err
	}

	return result.Manifest.Package, nil
}

func exportApk(outputDir, assemblyName string) (string, error) {
	// xamarin-sample-app/Droid/bin/Release/com.bitrise.xamarin.sampleapp.apk
	apks, err := filepath.Glob(filepath.Join(outputDir, "*.apk"))
	if err != nil {
		return "", fmt.Errorf("failed to find apk, error: %s", err)
	}

	rePattern := fmt.Sprintf(`(?i)%s.*signed.apk`, assemblyName)
	re := regexp.MustCompile(rePattern)

	filteredApks := []string{}
	for _, apk := range apks {
		if match := re.FindString(apk); match != "" {
			filteredApks = append(filteredApks, apk)
		}
	}

	if len(filteredApks) == 0 {
		rePattern := fmt.Sprintf(`%s.apk`, assemblyName)
		re := regexp.MustCompile(rePattern)

		for _, apk := range apks {
			if match := re.FindString(apk); match != "" {
				filteredApks = append(filteredApks, apk)
			}
		}

		if len(filteredApks) == 0 {
			filteredApks = apks
		}
	}

	if len(filteredApks) == 0 {
		return "", nil
	}

	return filteredApks[0], nil
}

func exportLatestXCArchiveFromXcodeArchives(assemblyName string) (string, error) {
	userHomeDir := os.Getenv("HOME")
	if userHomeDir == "" {
		return "", fmt.Errorf("failed to get user home dir")
	}
	xcodeArchivesDir := filepath.Join(userHomeDir, "Library/Developer/Xcode/Archives")
	if exist, err := pathutil.IsDirExists(xcodeArchivesDir); err != nil {
		return "", err
	} else if !exist {
		return "", fmt.Errorf("no default Xcode archive path found at: %s", xcodeArchivesDir)
	}

	return exportLatestXCArchive(xcodeArchivesDir, assemblyName)
}

// Sort path

// ByArchiveDate ...
type ByArchiveDate []string

// Len ...
func (d ByArchiveDate) Len() int {
	return len(d)
}

// Swap ...
func (d ByArchiveDate) Swap(i, j int) {
	d[i], d[j] = d[j], d[i]
}

func (d ByArchiveDate) Less(i, j int) bool {
	pths := []string{string(d[i]), string(d[j])}

	// compare directory name
	// $HOME/Library/Developer/Xcode/Archives/2016-10-07/
	// $HOME/Library/Developer/Xcode/Archives/2017-10-09/
	layout := "2006-01-02"

	dirDates := []time.Time{}
	for _, pth := range pths {
		dirPth := filepath.Dir(pth)
		dir := filepath.Base(dirPth)
		date, err := time.Parse(layout, dir)
		if err != nil {
			log.Error("failed to parse xcrachive dir name (%s) with layout (%s), error: %s", dir, layout, err)
			return false
		}

		dirDates = append(dirDates, date)
	}

	if dirDates[0].After(dirDates[1]) {
		return true
	}

	// compare file name
	// XamarinSampleApp.iOS 10-07-16 3.41 PM 2.xcarchive
	// XamarinSampleApp.iOS 10-09-16 3.41 PM.xcarchive
	layout = "01-02-06 3.04 PM"
	datePattern := `.* (?P<date>[0-9-]+ [0-9.]+ PM|AM)[ ]*(?P<count>|[0-9]+).xcarchive`
	re := regexp.MustCompile(datePattern)

	baseDates := []time.Time{}
	baseCounts := []int{}

	for _, pth := range pths {
		base := filepath.Base(pth)
		matches := re.FindStringSubmatch(base)
		if len(matches) == 3 {
			date, err := time.Parse(layout, matches[1])
			if err != nil {
				log.Error("failed to parse xcrachive file name (%s) with layout (%s), error: %s", matches[1], layout, err)
				return false
			}

			baseDates = append(baseDates, date)

			if matches[2] != "" {
				count, err := strconv.Atoi(matches[2])
				if err != nil {
					log.Error("failed to parse (%s) as int, error: %s", matches[2], err)
					return false
				}

				baseCounts = append(baseCounts, count)
			} else {
				baseCounts = append(baseCounts, 0)
			}
		}
	}

	if baseDates[0].After(baseDates[1]) {
		return true
	}

	if baseDates[0].Equal(baseDates[1]) && baseCounts[0] > baseCounts[1] {
		return true
	}

	return false
}

func exportLatestXCArchive(outputDir, assemblyName string) (string, error) {
	// $HOME/Library/Developer/Xcode/Archives/2016-10-07/XamarinSampleApp.iOS 10-07-16 3.41 PM 2.xcarchive
	pattern := filepath.Join(outputDir, "*", "*.xcarchive")
	archives, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to find xcarchive with pattern (%s), error: %s", pattern, err)
	}
	if len(archives) == 0 {
		return "", nil
	}

	rePattern := fmt.Sprintf(".*/%s.xcarchive", assemblyName)
	re := regexp.MustCompile(rePattern)

	filteredArchives := []string{}
	for _, archive := range archives {
		if match := re.FindString(archive); match != "" {
			filteredArchives = append(filteredArchives, archive)
		}
	}

	if len(filteredArchives) == 0 {
		filteredArchives = archives
	}

	if len(filteredArchives) == 0 {
		return "", nil
	}

	sort.Sort(ByArchiveDate(filteredArchives))

	return string(filteredArchives[0]), nil
}

// ByIpaDate ...
type ByIpaDate []string

// Len ...
func (d ByIpaDate) Len() int {
	return len(d)
}

// Swap ...
func (d ByIpaDate) Swap(i, j int) {
	d[i], d[j] = d[j], d[i]
}

func (d ByIpaDate) Less(i, j int) bool {
	pths := []string{string(d[i]), string(d[j])}

	// compare directory name
	// Multiplatform.iOS 2016-10-06 11-45-23
	// Multiplatform.iOS 2016-10-06 22-45-23
	layout := "2006-01-02 15-04-05"
	datePattern := `.* (?P<date>[0-9-]+-[0-9-]+-[0-9-]+ [0-9-]+-[0-9-]+-[0-9-]+)[ ]*(?P<count>[0-9]+|)`
	re := regexp.MustCompile(datePattern)

	dirDates := []time.Time{}
	dirDateCounts := []int{}

	for _, pth := range pths {
		dirPth := filepath.Dir(pth)
		dir := filepath.Base(dirPth)

		matches := re.FindStringSubmatch(dir)
		if len(matches) == 3 {
			date, err := time.Parse(layout, matches[1])
			if err != nil {
				log.Error("failed to parse ipa dir name (%s) with layout (%s), error: %s", matches[1], layout, err)
				return false
			}

			dirDates = append(dirDates, date)

			if matches[2] != "" {
				count, err := strconv.Atoi(matches[2])
				if err != nil {
					log.Error("failed to parse (%s) as int, error: %s", matches[2], err)
					return false
				}

				dirDateCounts = append(dirDateCounts, count)
			} else {
				dirDateCounts = append(dirDateCounts, 0)
			}
		}
	}

	if dirDates[0].After(dirDates[1]) {
		return true
	}

	if dirDates[0].Equal(dirDates[1]) && dirDateCounts[0] > dirDateCounts[1] {
		return true
	}

	return false
}

func exportLatestIpa(outputDir, assemblyName string) (string, error) {
	// Multiplatform/iOS/bin/iPhone/Release/Multiplatform.iOS 2016-10-06 11-45-23/Multiplatform.iOS.ipa
	pattern := filepath.Join(outputDir, "*", "*.ipa")
	ipas, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to find ipa with pattern (%s), error: %s", pattern, err)
	}
	if len(ipas) == 0 {
		return "", nil
	}

	rePattern := fmt.Sprintf("%s .*/%s.ipa", assemblyName, assemblyName)
	re := regexp.MustCompile(rePattern)

	filteredIpas := []string{}
	for _, ipa := range ipas {
		if match := re.FindString(ipa); match != "" {
			filteredIpas = append(filteredIpas, ipa)
		}
	}

	if len(filteredIpas) == 0 {
		filteredIpas = ipas
	}

	if len(filteredIpas) == 0 {
		return "", nil
	}

	sort.Sort(ByIpaDate(filteredIpas))

	return string(filteredIpas[0]), nil
}

func exportAppDSYM(outputDir, assemblyName string) (string, error) {
	// Multiplatform/iOS/bin/iPhone/Release/Multiplatform.iOS.app.dSYM
	pattern := filepath.Join(outputDir, "*.app.dSYM")
	dSYMs, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to find dsym with pattern (%s), error: %s", pattern, err)
	}
	if len(dSYMs) == 0 {
		return "", nil
	}

	rePattern := fmt.Sprintf("%s.app.dSYM", assemblyName)
	re := regexp.MustCompile(rePattern)

	filteredDsyms := []string{}
	for _, dSYM := range dSYMs {
		if match := re.FindString(dSYM); match != "" {
			filteredDsyms = append(filteredDsyms, dSYM)
		}
	}

	if len(filteredDsyms) == 0 {
		filteredDsyms = dSYMs
	}

	if len(filteredDsyms) == 0 {
		return "", nil
	}

	return filteredDsyms[0], nil
}

func exportFrameworkDSYMs(outputDir string) ([]string, error) {
	// Multiplatform/iOS/bin/iPhone/Release/TTTAttributedLabel.framework.dSYM
	pattern := filepath.Join(outputDir, "*.framework.dSYM")
	dSYMs, err := filepath.Glob(pattern)
	if err != nil {
		return []string{}, fmt.Errorf("failed to find dsym with pattern (%s), error: %s", pattern, err)
	}
	return dSYMs, nil
}

func exportPKG(outputDir, assemblyName string) (string, error) {
	// Multiplatform/Mac/bin/Release/Multiplatform.Mac-1.0.pkg
	pattern := filepath.Join(outputDir, "*.pkg")
	pkgs, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to find pkg with pattern (%s), error: %s", pattern, err)
	}
	if len(pkgs) == 0 {
		return "", nil
	}
	rePattern := fmt.Sprintf("%s.*.pkg", assemblyName)
	re := regexp.MustCompile(rePattern)

	filteredPKGs := []string{}
	for _, pkg := range pkgs {
		if match := re.FindString(pkg); match != "" {
			filteredPKGs = append(filteredPKGs, pkg)
		}
	}

	if len(filteredPKGs) == 0 {
		filteredPKGs = pkgs
	}

	if len(filteredPKGs) == 0 {
		return "", nil
	}

	return filteredPKGs[0], nil
}

func exportApp(outputDir, assemblyName string) (string, error) {
	// Multiplatform/Mac/bin/Release/Multiplatform.Mac.app
	pattern := filepath.Join(outputDir, "*.app")
	apps, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to find app with pattern (%s), error: %s", pattern, err)
	}
	if len(apps) == 0 {
		return "", nil
	}
	rePattern := fmt.Sprintf("%s.app", assemblyName)
	re := regexp.MustCompile(rePattern)

	filteredAPPs := []string{}
	for _, app := range apps {
		if match := re.FindString(app); match != "" {
			filteredAPPs = append(filteredAPPs, app)
		}
	}

	if len(filteredAPPs) == 0 {
		filteredAPPs = apps
	}

	if len(filteredAPPs) == 0 {
		return "", nil
	}

	return filteredAPPs[0], nil
}
