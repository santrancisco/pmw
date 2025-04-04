package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
    "flag"
	"path/filepath"
	"regexp"
	"strings"
	"time"
    "os/signal"
	"syscall"
	"sort"
)

var ColorReset = "\033[0m" 
var ColorRed = "\033[31m" 
var ColorGreen = "\033[32m" 
var ColorYellow = "\033[33m" 
var ColorBlue = "\033[34m" 
var ColorMagenta = "\033[35m" 
var ColorCyan = "\033[36m" 
var ColorGray = "\033[37m" 
var ColorWhite = "\033[97m"

// AllowedOrgs: Keeping the list of organisation where we accept version tagging for their workflow
// AcceptedMapping: List of previously accepted versions & their commit hashes.
type Config struct {
	AllowedOrgs     []string          `json:"allowedOrgs"`
	AcceptedMapping map[string]string `json:"acceptedMapping"`
}

var configFile = ".github/pmw-config.json"
var verbose = false
var config Config

func loadConfig() error {
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No configuration found. Please enter a comma-separated list of allowed organizations:")
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			orgs := strings.Split(input, ",")
			for i, org := range orgs {
				orgs[i] = strings.TrimSpace(org)
			}
			config = Config{
				AllowedOrgs:     orgs,
				AcceptedMapping: make(map[string]string),
			}
			// Ensure .github folder exists
			os.MkdirAll(".github", os.ModePerm)
			return saveConfig()
		}
		return err
	}
	return json.Unmarshal(data, &config)
}

// saveConfig saves the current configuration into the config file.
func saveConfig() error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(configFile, data, 0644)
}


type GitRefResponse struct {
	Object struct {
		Type string `json:"type"`
		Sha  string `json:"sha"`
	} `json:"object"`
}

type GitTagResponse struct {
	Object struct {
		Type string `json:"type"`
		Sha  string `json:"sha"`
	} `json:"object"`
}

// Resolving tag reference via github API
func resolveTag(owner, repo, sha string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/tags/%s", owner, repo, sha)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
	}
	var tagResponse GitTagResponse
	if err := json.NewDecoder(resp.Body).Decode(&tagResponse); err != nil {
		return "", err
	}
	return tagResponse.Object.Sha, nil
}


func compareVersions(v1, v2 string) int {
    parts1 := strings.Split(v1, ".")
    parts2 := strings.Split(v2, ".")
    maxLen := len(parts1)
    if len(parts2) > maxLen {
        maxLen = len(parts2)
    }
    for i := 0; i < maxLen; i++ {
        var num1, num2 int
        if i < len(parts1) {
            fmt.Sscanf(parts1[i], "%d", &num1)
        }
        if i < len(parts2) {
            fmt.Sscanf(parts2[i], "%d", &num2)
        }
        if num1 > num2 {
            return 1
        } else if num1 < num2 {
            return -1
        }
    }
    return 0
}

// Turn out when user put in v2, github workflow will find the latest version that has prefix "v2".. .so it could be v2.x.x, v2.x
// As such we will need to go through all tags and compare the versions.
func findLatestVersionTag(owner, repo, prefix string) (string, error) {
    url := fmt.Sprintf("https://api.github.com/repos/%s/%s/tags", owner, repo)
    resp, err := http.Get(url)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return "", fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
    }
    var tags []struct {
        Name   string `json:"name"`
        Commit struct {
            Sha string `json:"sha"`
        } `json:"commit"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
        return "", err
    }
    var filtered []string
    for _, t := range tags {
        if strings.HasPrefix(t.Name, prefix) {
            filtered = append(filtered, t.Name)
        }
    }
    if len(filtered) == 0 {
        return "", fmt.Errorf("no tags found with prefix %s", prefix)
    }
    // Sort filtered tags in descending order (latest first) using semantic version comparison.
    sort.Slice(filtered, func(i, j int) bool {
        v1 := strings.TrimPrefix(filtered[i], "v")
        v2 := strings.TrimPrefix(filtered[j], "v")
        return compareVersions(v1, v2) > 0
    })
    latestTag := filtered[0]
    for _, t := range tags {
        if t.Name == latestTag {
            return t.Commit.Sha, nil
        }
    }
    return "", fmt.Errorf("could not resolve latest tag for prefix %s", prefix)
}

// Getting commit sha for version tags, iterate through nested tag if neccessary
// for workflow pin to master, we just get the hash of master branch
func getCommitSha(owner, repo, tag string) (string, error) {
	var orginUrl string
	if tag == "master" {
		orginUrl = fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/heads/master", owner, repo)
	} else if tag == "main" {
		orginUrl = fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/heads/main", owner, repo)
	}  else {
		return findLatestVersionTag(owner, repo, tag)
	}
	resp, err := http.Get(orginUrl)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
	}
	var tagResponse GitTagResponse
	if err := json.NewDecoder(resp.Body).Decode(&tagResponse); err != nil {
		return "", err
	}
	return tagResponse.Object.Sha, nil
}

// isAllowedOrg returns true if the given owner is in the allowed organizations list.
func isAllowedOrg(owner string) bool {
	for _, org := range config.AllowedOrgs {
		if strings.EqualFold(owner, org) {
			return true
		}
	}
	return false
}

//   We are searching for workflow usages:
//   uses: owner/repo@tag
var usageRegex = regexp.MustCompile(`uses:\s*([^/]+)/([^@]+)@(\S+)`)

// processFile go through each line in workflow
func processFile(filePath string) error {
    if verbose == true {
        fmt.Printf("[+] Processing %s\n", filePath)
    }
	inputBytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(inputBytes), "\n")
	changed := false

	for i, line := range lines {
		matches := usageRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		owner, repo, tag := matches[1], matches[2], matches[3]

		// Skip if owner is allowed or tag is already a commit hash.
		if isAllowedOrg(owner) || regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(tag) {
			continue
		}

		key := fmt.Sprintf("%s/%s@%s", owner, repo, tag)
		var commitSha string
		var ok bool

		if commitSha, ok = config.AcceptedMapping[key]; ok {
            fmt.Println("-----------------------")
			fmt.Printf(ColorGreen+"Previously accepted for %s: using commit %s\n"+ColorReset, key, commitSha)
		} else {
            fmt.Println("-----------------------")
			commitSha, err = getCommitSha(owner, repo, tag)
			if err != nil {
				fmt.Printf("Error retrieving commit SHA for %s: %v\n", key, err)
				continue
			}
			var versionTag string
            var checkUrl string
			if tag == "master" {
				versionTag = fmt.Sprintf("#master-%s", time.Now().Format("2006-01-02"))
                checkUrl = fmt.Sprintf("https://github.com/%s/%s/commits/master/", owner, repo)
			} else {
				versionTag = fmt.Sprintf("#%s", tag)
			    checkUrl = fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", owner, repo, tag)
			}
            newUsage := fmt.Sprintf("uses: %s/%s@%s %s", owner, repo, commitSha, versionTag)
			fmt.Printf("[.]In File: %s\n"+ColorRed+"[-]Old: %s (Check URL: %s)\n"+ColorReset+ColorBlue+"[+]New: %s\n"+ColorReset+"Choose option: (y)es, (n)o, (a)dd to allowedOrgs, (q)uit: ", filePath, strings.TrimSpace(line), checkUrl, newUsage)

            reader := bufio.NewReader(os.Stdin)
            answer, _ := reader.ReadString('\n')
            answer = strings.TrimSpace(strings.ToLower(answer))
            switch answer {
            case "y":
                config.AcceptedMapping[key] = commitSha
                commitSha = config.AcceptedMapping[key]
                lines[i] = newUsage
                changed = true
            case "n":
                continue
            case "a":
                config.AllowedOrgs = append(config.AllowedOrgs, owner)
                fmt.Printf("Added %s to allowed organizations.\n", owner)
                continue
            case "q":
                fmt.Println("Quitting processing...")
                if err := saveConfig(); err != nil {
                    fmt.Printf("Error saving config: %v\n", err)
                }
                os.Exit(0)
            default:
                fmt.Println("Invalid option, skipping.")
                continue
            }
		}

		// Even if the mapping was already accepted previously, ensure the version tag comment is updated on the day we run it again
		var versionTag string
		if tag == "master" {
			versionTag = fmt.Sprintf("#master-%s", time.Now().Format("2006-01-02"))
		} else {
			versionTag = fmt.Sprintf("#%s", tag)
		}
		leadingWhitespace := ""
		for _, r := range line {
			if r == ' ' || r == '\t' {
				leadingWhitespace += string(r)
			} else {
				break
			}
		}
		lines[i] = fmt.Sprintf("%suses: %s/%s@%s %s",leadingWhitespace, owner, repo, commitSha, versionTag)
		changed = true
	}
	if changed {
		newContent := strings.Join(lines, "\n")
		err = ioutil.WriteFile(filePath, []byte(newContent), 0644)
		if err != nil {
			return err
		}
		fmt.Printf(ColorGreen+"Updated file: %s\n"+ColorReset, filePath)
	}
	return nil
}

func main() {
	// Allow using config file but by default, we save our config right into .github folder.
    configPath := flag.String("c", ".github/pmw-config.json", "Path to configuration file")
    verboseMode := flag.Bool("v", false, "Verbose mode")
    flag.Parse()
    configFile = *configPath
    verbose = *verboseMode

	if err := loadConfig(); err != nil {
		fmt.Println("Error loading config:", err)
		return
	}

	// Catching Ctrl+C and SIGTERM and save config halfway
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		fmt.Printf("Received signal: %v, saving config and exiting...\n", sig)
		if err := saveConfig(); err != nil {
			fmt.Printf("Error saving config: %v\n", err)
		}
		os.Exit(0)
	}()

	err := filepath.Walk(".github/workflows", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".yml") || strings.HasSuffix(info.Name(), ".yaml") {
			if err := processFile(path); err != nil {
				fmt.Printf("Error processing file %s: %v\n", path, err)
			}
		}
		return nil
	})
	if err != nil {
		fmt.Println("Error walking directory:", err)
	}
	if err := saveConfig(); err != nil {
		fmt.Println("Error saving config:", err)
	}
}