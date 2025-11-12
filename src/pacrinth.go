package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"archive/zip"
	"github.com/pelletier/go-toml"
	"gopkg.in/yaml.v3"
	"time"
)
var ignoredDependencies = map[string]bool{
	"fabricloader": true,
	"quilt-loader": true,
	"minecraft":    true,
	"java":         true,
	"fabric-renderer-api-v1": true,
	"fabric-rendering-fluids-v1": true,
	"fabric-resource-loader-v0": true,
	"fabric-block-view-api-v2": true,
}
type ModrinthMod struct {
	Slug string `json:"slug"`
}
var downloadedMods = make(map[string]bool)
var client = &http.Client{}
func getMinecraftFolder() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", ".minecraft")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "minecraft")
	default:
		return filepath.Join(home, ".minecraft")
	}
}

func getFolder(folderType string) string {
	switch folderType {
	case "mods":
		return filepath.Join(getMinecraftFolder(), "mods")
	case "resourcepacks":
		return filepath.Join(getMinecraftFolder(), "resourcepacks")
	case "shaders":
		return filepath.Join(getMinecraftFolder(), "shaders")
	case "datapacks":
		return filepath.Join(getMinecraftFolder(), "datapacks")
	case "modpacks":
		return filepath.Join(getMinecraftFolder(), "modpacks")
	case "plugins":
		return filepath.Join(getMinecraftFolder(), "plugins")
	default:
		return getMinecraftFolder()
	}
}

func getProjectVersions(projectSlug string) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("https://api.modrinth.com/v2/project/%s/version", projectSlug)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected response code %d", resp.StatusCode)
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func downloadFile(url, saveDir string) (string, error) {
	parts := strings.Split(url, "/")
	fileName := parts[len(parts)-1]
	if _, err := os.Stat(saveDir); os.IsNotExist(err) {
		os.MkdirAll(saveDir, os.ModePerm)
	}
	outputPath := filepath.Join(saveDir, fileName)
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to download file: %s", url)
	}
	out, err := os.Create(outputPath)
	if err != nil {
		return "", err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", err
	}
	return outputPath, nil
}

func downloadGeneric(projectSlug, mcVersion, loader, folder string) (string, error) {
	versions, err := getProjectVersions(projectSlug)
	if err != nil {
		return "", err
	}
	var targetVersion map[string]interface{}
	for _, v := range versions {
		if mcVersion != "" {
			matched := false
			for _, gv := range v["game_versions"].([]interface{}) {
				if gv.(string) == mcVersion {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if loader != "" {
			matched := false
			for _, l := range v["loaders"].([]interface{}) {
				if strings.EqualFold(l.(string), loader) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		targetVersion = v
		break
	}
	if targetVersion == nil {
		return "", fmt.Errorf("no matching versions found for project %s", projectSlug)
	}
	files := targetVersion["files"].([]interface{})
	fileURL := files[0].(map[string]interface{})["url"].(string)
	return downloadFile(fileURL, folder)
}

func downloadMod(projectSlug, mcVersion, loader string) (string, error) {
	return downloadGeneric(projectSlug, mcVersion, loader, getFolder("mods"))
}

func downloadModpack(projectSlug, mcVersion, loader string) error {
	_, err := downloadGeneric(projectSlug, mcVersion, loader, getFolder("modpacks"))
	return err
}

func downloadShader(projectSlug, mcVersion, loader string) error {
	_, err := downloadGeneric(projectSlug, mcVersion, loader, getFolder("shaders"))
	return err
}

func downloadResourcePack(projectSlug, mcVersion string) error {
	_, err := downloadGeneric(projectSlug, mcVersion, "", getFolder("resourcepacks"))
	return err
}

func downloadDataPack(projectSlug, mcVersion string) error {
	_, err := downloadGeneric(projectSlug, mcVersion, "", getFolder("datapacks"))
	return err
}

func projectExists(projectSlug string) bool {
	url := fmt.Sprintf("https://api.modrinth.com/v2/project/%s", projectSlug)
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func resolveDependencySlug(depSlug string) string {
	variants := []string{
		depSlug, depSlug + "-api", depSlug + "-mod", depSlug + "-mc",
		strings.ReplaceAll(depSlug, "-", "_"),
		strings.ReplaceAll(depSlug, "-", ""),
		strings.ReplaceAll(depSlug, "_", "-"),
		strings.ReplaceAll(depSlug, "_", ""),
	}
	for _, v := range variants {
		if projectExists(v) {
			return v
		}
	}
	return ""
}

func getDependenciesFromAPI(projectSlug, mcVersion, loader string) ([]string, error) {
	versions, err := getProjectVersions(projectSlug)
	if err != nil {
		return nil, err
	}

	var targetVersion map[string]interface{}
	for _, v := range versions {
		if mcVersion != "" {
			matched := false
			for _, gv := range v["game_versions"].([]interface{}) {
				if gv.(string) == mcVersion {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if loader != "" {
			matched := false
			for _, l := range v["loaders"].([]interface{}) {
				if strings.EqualFold(l.(string), loader) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		targetVersion = v
		break
	}

	if targetVersion == nil {
		return nil, fmt.Errorf("no matching versions found for project %s", projectSlug)
	}

	dependencies := []string{}
	if deps, ok := targetVersion["dependencies"].([]interface{}); ok {
		for _, dep := range deps {
			if depMap, ok := dep.(map[string]interface{}); ok {
				// Only include required dependencies, skip optional if you want
				if depMap["dependency_type"].(string) == "required" {
					dependencies = append(dependencies, fmt.Sprintf("%s:%s@%s", depMap["project_id"], loader, mcVersion))
				}
			}
		}
	}
	return dependencies, nil
}
func downloadModWithDeps(projectSlug, mcVersion, loader string) {
	projectSlug = IDToSlug(projectSlug)
	projectSlug = strings.ToLower(projectSlug)
	if downloadedMods[projectSlug] {
		return
	}
	downloadedMods[projectSlug] = true
	filePath, err := downloadMod(projectSlug, mcVersion, loader)
	if err != nil {
		fmt.Println("Error downloading mod: " + projectSlug, err)
		return
	}
	fmt.Println("Downloaded:", filepath.Base(filePath))
	depsAPI, err := getDependenciesFromAPI(projectSlug, mcVersion, loader)
	if err != nil {
		depsAPI = []string{}
	}

	depsJar, err := getDependenciesFromJar(filePath, mcVersion, loader)
	if err != nil {
		depsJar = []string{}
	}
	allDeps := append(depsAPI, depsJar...)

	if len(allDeps) > 0 {
		fmt.Println("Auto-handled dependencies:")
	for _, dep := range allDeps {
    	parts := strings.Split(dep, "@")
    	modPart := strings.Split(parts[0], ":")[0]

    	if ignoredDependencies[modPart] {
        continue
    	}

    	resolved := resolveDependencySlug(modPart)
    	if resolved != "" {
        downloadModWithDeps(resolved, mcVersion, loader)
    	} else {
        fmt.Println("Unresolved dependency:", modPart)
    	}
	}

	}
}
func hasNameConflictModPackMod(projectSlug string) bool {
	url := fmt.Sprintf("https://api.modrinth.com/v2/project/%s", projectSlug)
	resp, err := client.Get(url)
	if err != nil || resp.StatusCode != 200 {
		return false
	}
	defer resp.Body.Close()
	var project map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&project)
	ptype := project["project_type"].(string)
	return strings.ToLower(ptype) != "mod" && strings.ToLower(ptype) != "modpack"
}

func hasNameConflictResourcePackDatapack(projectSlug string) bool {
	url := fmt.Sprintf("https://api.modrinth.com/v2/project/%s", projectSlug)
	resp, err := client.Get(url)
	if err != nil || resp.StatusCode != 200 {
		return false
	}
	defer resp.Body.Close()

	var project map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&project); err != nil {
		return false
	}

	ptype, ok := project["project_type"].(string)
	if !ok {
		return false
	}

	ptype = strings.ToLower(ptype)
	return ptype != "resourcepack" && ptype != "datapack"
}


func readLine(prompt string) string {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	return strings.ToLower(strings.TrimSpace(scanner.Text()))
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage:\npacrinth mod@loader\npacrinth mod:version@loader\npacrinth resourcepack@version\npacrinth shader@shaderloader\npacrinth modpack@loader\npacrinth datapack@version")
		return
	}

	for _, arg := range os.Args[1:] {
		input := strings.ToLower(arg)

		if strings.Contains(input, "@f") || strings.Contains(input, "@neo") || strings.Contains(input, "@quilt") {
			info := strings.FieldsFunc(input, func(r rune) bool { return r == '@' || r == ':' })
			if hasNameConflictModPackMod(info[0]) {
				choice := readLine("Conflict detected! Is this a mod or a modpack? (mod/modpack): ")
				if choice == "modpack" {
					if len(info) > 2 {
						downloadModpack(info[0], info[1], info[2])
					} else {
						downloadModpack(info[0], "", info[1])
					}
				} else {
					if len(info) > 2 {
						downloadModWithDeps(info[0], info[1], info[2])
					} else {
						downloadModWithDeps(info[0], "", info[1])
					}
				}
			} else {
				if len(info) > 2 {
					downloadModWithDeps(info[0], info[1], info[2])
				} else {
					downloadModWithDeps(info[0], "", info[1])
				}
			}
		} else if strings.Contains(input, "@o") || strings.Contains(input, "@iris") {
			info := strings.FieldsFunc(input, func(r rune) bool { return r == '@' || r == ':' })
			if len(info) > 2 {
				downloadShader(info[0], info[2], info[1])
			} else if len(info) > 1 {
				downloadShader(info[0], "", info[1])
			} else {
				downloadShader(info[0], "", "")
			}
		} else {
			info := strings.Split(input, "@")
			if hasNameConflictResourcePackDatapack(info[0]) {
				choice := readLine("Conflict detected! Is this a resourcepack or datapack? (resource/datapack): ")
				if choice == "datapack" {
					if len(info) > 1 {
						downloadDataPack(info[0], info[1])
					} else {
						downloadDataPack(info[0], "")
					}
				} else {
					if len(info) > 1 {
						downloadResourcePack(info[0], info[1])
					} else {
						downloadResourcePack(info[0], "")
					}
				}
			} else {
				if len(info) > 1 {
					downloadResourcePack(info[0], info[1])
				} else {
					downloadResourcePack(info[0], "")
				}
			}
		}
	}
}
	
func getDependenciesFromJar(jarPath, mcVersion, loader string) ([]string, error) {
	dependencies := []string{}

	r, err := zip.OpenReader(jarPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	entryMap := make(map[string]*zip.File)
	for _, f := range r.File {
		entryMap[strings.ToLower(f.Name)] = f
	}
	modJsonFiles := []string{"fabric.mod.json", "quilt.mod.json"}
	for _, name := range modJsonFiles {
		if f, ok := entryMap[strings.ToLower(name)]; ok {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			var meta map[string]interface{}
			if err := json.NewDecoder(rc).Decode(&meta); err == nil {
				if depends, ok := meta["depends"].(map[string]interface{}); ok {
					for dep := range depends {
						dependencies = append(dependencies, fmt.Sprintf("%s:%s@%s", dep, loader, mcVersion))
					}
				}
			}
			rc.Close()
		}
	}
	tomlFiles := []string{"meta-inf/mods.toml", "meta-inf/neoforge.mods.toml"}
	for _, name := range tomlFiles {
		if f, ok := entryMap[strings.ToLower(name)]; ok {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			content, _ := io.ReadAll(rc)
			var tomlData map[string]interface{}
			if err := toml.Unmarshal(content, &tomlData); err == nil {
				if mods, ok := tomlData["mods"].([]interface{}); ok {
					for _, modObj := range mods {
						modMap, ok := modObj.(map[string]interface{})
						if !ok {
							continue
						}
						if deps, ok := modMap["dependencies"].([]interface{}); ok {
							for _, depObj := range deps {
								depMap, ok := depObj.(map[string]interface{})
								if ok {
									if modId, ok := depMap["modId"].(string); ok {
										dependencies = append(dependencies, fmt.Sprintf("%s:%s@%s", modId, loader, mcVersion))
									}
								}
							}
						}
					}
				}
			}
			rc.Close()
		}
	}
	if f, ok := entryMap["plugin.yml"]; ok {
		rc, err := f.Open()
		if err == nil {
			var yamlData map[string]interface{}
			decoder := yaml.NewDecoder(rc)
			if err := decoder.Decode(&yamlData); err == nil {
				keys := []string{"depend", "softdepend"}
				for _, key := range keys {
					if val, ok := yamlData[key]; ok {
						switch v := val.(type) {
						case []interface{}:
							for _, dep := range v {
								dependencies = append(dependencies, fmt.Sprintf("%v:%s@%s", dep, loader, mcVersion))
							}
						case string:
							dependencies = append(dependencies, fmt.Sprintf("%s:%s@%s", v, loader, mcVersion))
						}
					}
				}
			}
			rc.Close()
		}
	}

	return dependencies, nil
}
func IDToSlug(id string) string {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://api.modrinth.com/v2/project/%s", id)

	resp, err := client.Get(url)
	if err != nil {
		return id
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return id
	}

	var mod ModrinthMod
	if err := json.NewDecoder(resp.Body).Decode(&mod); err != nil {
		return id
	}

	if mod.Slug == "" {
		return id
	}

	return mod.Slug;
}