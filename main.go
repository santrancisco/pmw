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

// Getting commit sha for version tags, iterate through nested tag if neccessary
// for workflow pin to master, we just get the hash of master branch
func getCommitSha(owner, repo, tag string) (string, error) {
	var orginUrl string
	if tag == "master" {
		orginUrl = fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/heads/master", owner, repo)
	} else {
		orginUrl = fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/tags/%s", owner, repo, tag)
	}
	resp, err := http.Get(orginUrl)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
	}
	var ref GitRefResponse
	if err := json.NewDecoder(resp.Body).Decode(&ref); err != nil {
		return "", err
	}

	commitSha := ref.Object.Sha
	weirdMapping := false

	// If the type is "tag", go deeper
	for ref.Object.Type == "tag" {
		weirdMapping = true
		resolvedSha, err := resolveTag(owner, repo, commitSha)
		if err != nil {
			return "", err
		}
		commitSha = resolvedSha

		// Check if the resolved object is still a tag.
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/tags/%s", owner, repo, commitSha)
		resp2, err := http.Get(url)
		if err != nil {
			return "", err
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != 200 {
			break
		}
		var tagResp GitTagResponse
		if err := json.NewDecoder(resp2.Body).Decode(&tagResp); err != nil {
			return "", err
		}
		// If still a tag, update and continue the loop.
		if tagResp.Object.Type == "tag" {
			ref.Object.Type = tagResp.Object.Type
		} else {
			break
		}
	}
	if weirdMapping {
		fmt.Printf(ColorMagenta+"Note: Nested tag mapping encountered for %s/%s@%s, resolved to commit %s\nYou can check nested mapping at:%s\n"+ColorReset, owner, repo, tag, commitSha, orginUrl)
	}
	return commitSha, nil
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
		lines[i] = fmt.Sprintf("uses: %s/%s@%s %s", owner, repo, commitSha, versionTag)
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