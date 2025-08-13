package yarninstall

import (
	"bytes"
    "encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paketo-buildpacks/packit/v2/fs"
	"github.com/paketo-buildpacks/packit/v2/pexec"
	"github.com/paketo-buildpacks/packit/v2/scribe"
)

//go:generate faux --interface Summer --output fakes/summer.go
type Summer interface {
	Sum(paths ...string) (string, error)
}

//go:generate faux --interface Executable --output fakes/executable.go
type Executable interface {
	Execute(pexec.Execution) error
}

type YarnInstallProcess struct {
	executable Executable
	summer     Summer
	logger     scribe.Emitter
}

func NewYarnInstallProcess(executable Executable, summer Summer, logger scribe.Emitter) YarnInstallProcess {
	return YarnInstallProcess{
		executable: executable,
		summer:     summer,
		logger:     logger,
	}
}

func (ip YarnInstallProcess) ShouldRun(workingDir string, metadata map[string]interface{}) (run bool, sha string, err error) {
	ip.logger.Subprocess("Process inputs:")

	_, err = os.Stat(filepath.Join(workingDir, "yarn.lock"))
	if os.IsNotExist(err) {
		ip.logger.Action("yarn.lock -> Not found")
		ip.logger.Break()
		return true, "", nil
	} else if err != nil {
		return true, "", fmt.Errorf("unable to read yarn.lock file: %w", err)
	}

	ip.logger.Action("yarn.lock -> Found")
	ip.logger.Break()

    buffer := bytes.NewBuffer(nil)

    if isYarnBerry(workingDir) {
        // Yarn 4+: no `yarn config list`; include `.yarnrc.yml` contents if present
        if data, readErr := os.ReadFile(filepath.Join(workingDir, ".yarnrc.yml")); readErr == nil {
            _, _ = buffer.Write(data)
        }
    } else {
        err = ip.executable.Execute(pexec.Execution{
            Args:   []string{"config", "list", "--silent"},
            Stdout: buffer,
            Stderr: buffer,
            Dir:    workingDir,
        })
        if err != nil {
            return true, "", fmt.Errorf("failed to execute yarn config output:\n%s\nerror: %s", buffer.String(), err)
        }
    }

	nodeEnv := os.Getenv("NODE_ENV")
	buffer.WriteString(nodeEnv)

	file, err := os.CreateTemp("", "config-file")
	if err != nil {
		return true, "", fmt.Errorf("failed to create temp file for %s: %w", file.Name(), err)
	}
	defer file.Close()

	_, err = file.Write(buffer.Bytes())
	if err != nil {
		return true, "", fmt.Errorf("failed to write temp file for %s: %w", file.Name(), err)
	}

	sum, err := ip.summer.Sum(filepath.Join(workingDir, "yarn.lock"), filepath.Join(workingDir, "package.json"), file.Name())
	if err != nil {
		return true, "", fmt.Errorf("unable to sum config files: %w", err)
	}

	prevSHA, ok := metadata["cache_sha"].(string)
	if (ok && sum != prevSHA) || !ok {
		return true, sum, nil
	}

	return false, "", nil
}

func (ip YarnInstallProcess) SetupModules(workingDir, currentModulesLayerPath, nextModulesLayerPath string) (string, error) {
	if currentModulesLayerPath != "" {
		err := fs.Copy(filepath.Join(currentModulesLayerPath, "node_modules"), filepath.Join(nextModulesLayerPath, "node_modules"))
		if err != nil {
			return "", fmt.Errorf("failed to copy node_modules directory: %w", err)
		}

	} else {

		file, err := os.Lstat(filepath.Join(workingDir, "node_modules"))
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("failed to stat node_modules directory: %w", err)
			}

		}

		if file != nil && file.Mode()&os.ModeSymlink == os.ModeSymlink {
			err = os.RemoveAll(filepath.Join(workingDir, "node_modules"))
			if err != nil {
				//not tested
				return "", fmt.Errorf("failed to remove node_modules symlink: %w", err)
			}
		}

		err = os.MkdirAll(filepath.Join(workingDir, "node_modules"), os.ModePerm)
		if err != nil {
			//not directly tested
			return "", fmt.Errorf("failed to create node_modules directory: %w", err)
		}

		// err = fs.Move(filepath.Join(workingDir, "node_modules"), filepath.Join(nextModulesLayerPath, "node_modules"))
		// if err != nil {
		// 	return "", fmt.Errorf("failed to move node_modules directory to layer: %w", err)
		// }

		// err = os.Symlink(filepath.Join(nextModulesLayerPath, "node_modules"), filepath.Join(workingDir, "node_modules"))
		// if err != nil {
		// 	return "", fmt.Errorf("failed to symlink node_modules into working directory: %w", err)
		// }
	}

	return nextModulesLayerPath, nil
}

// The build process here relies on yarn install ... --frozen-lockfile note that
// even if we provide a node_modules directory we must run a 'yarn install' as
// this is the ONLY way to rebuild native extensions.
func (ip YarnInstallProcess) Execute(workingDir, modulesLayerPath string, launch bool) error {
	environment := os.Environ()
	environment = append(environment, fmt.Sprintf("PATH=%s%c%s", os.Getenv("PATH"), os.PathListSeparator, filepath.Join("node_modules", ".bin")))

    buffer := bytes.NewBuffer(nil)

    isBerry := isYarnBerry(workingDir)
    var err error
    if !isBerry {
        err = ip.executable.Execute(pexec.Execution{
            Args:   []string{"config", "get", "yarn-offline-mirror"},
            Stdout: buffer,
            Stderr: buffer,
            Env:    environment,
            Dir:    workingDir,
        })
        if err != nil {
            return fmt.Errorf("failed to execute yarn config output:\n%s\nerror: %s", buffer.String(), err)
        }
    }

    installArgs := []string{"install"}

    if isBerry {
        installArgs = append(installArgs, "--immutable")
    } else {
        installArgs = append(installArgs, "--ignore-engines")
        installArgs = append(installArgs, "--frozen-lockfile")
    }

	if !launch {
        if isBerry {
            // Yarn 4 does not support --production flag; ensure devDependencies are installed
            // by explicitly setting NODE_ENV to development for the install step.
            environment = append(environment, "NODE_ENV=development")
        } else {
            installArgs = append(installArgs, "--production", "false")
        }
	}

    // Determine offline install behavior
    if isBerry {
        // For Yarn 2+/4, prefer project cache at .yarn/cache when present
        berryCacheDir := filepath.Join(workingDir, ".yarn", "cache")
        if info, statErr := os.Stat(berryCacheDir); statErr == nil && info.IsDir() {
            installArgs = append(installArgs, "--offline")
        } else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
            return fmt.Errorf("failed to confirm existence of berry cache directory: %w", statErr)
        }
    } else {
        // Parse yarn config get yarn-offline-mirror output (Yarn 1 only)
        // in case there are any warning lines in the output like:
        // warning You don't appear to have an internet connection.
        var offlineMirrorDir string
        for _, line := range strings.Split(buffer.String(), "\n") {
            if strings.HasPrefix(strings.TrimSpace(line), "/") {
                offlineMirrorDir = strings.TrimSpace(line)
                break
            }
        }
        info, statErr := os.Stat(offlineMirrorDir)
        if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
            return fmt.Errorf("failed to confirm existence of offline mirror directory: %w", statErr)
        }

        if info != nil && info.IsDir() {
            installArgs = append(installArgs, "--offline")
        }
    }

	// installArgs = append(installArgs, "--modules-folder", filepath.Join(modulesLayerPath, "node_modules"))
	ip.logger.Subprocess("Running 'yarn %s'", strings.Join(installArgs, " "))

	err = ip.executable.Execute(pexec.Execution{
		Args:   installArgs,
		Env:    environment,
		Stdout: ip.logger.ActionWriter,
		Stderr: ip.logger.ActionWriter,
		Dir:    workingDir,
	})
	if err != nil {
		return fmt.Errorf("failed to execute yarn install: %w", err)
	}

		// Copy node_modules to layers folder for caching purpose
		err = fs.Copy(filepath.Join(workingDir, "node_modules"), filepath.Join(modulesLayerPath, "node_modules"))
		if err != nil {
			fmt.Println(err)
		}
		fmt.Println("Copy successful.")


	return nil
}

// isYarnBerry returns true if the project appears to use Yarn Berry (v2+) including Yarn 4.
// Heuristics:
// - Presence of .yarnrc.yml
// - Presence of .yarn/ directory
// - package.json contains packageManager starting with "yarn@" and major version >= 2
func isYarnBerry(workingDir string) bool {
    // .yarnrc.yml present
    if _, err := os.Stat(filepath.Join(workingDir, ".yarnrc.yml")); err == nil {
        return true
    }

    // .yarn directory present
    if info, err := os.Stat(filepath.Join(workingDir, ".yarn")); err == nil && info.IsDir() {
        return true
    }

    // package.json packageManager field
    pkgPath := filepath.Join(workingDir, "package.json")
    if data, err := os.ReadFile(pkgPath); err == nil {
        var pkg struct {
            PackageManager string `json:"packageManager"`
        }
        if jsonErr := json.Unmarshal(data, &pkg); jsonErr == nil {
            if strings.HasPrefix(pkg.PackageManager, "yarn@") {
                ver := strings.TrimPrefix(pkg.PackageManager, "yarn@")
                // Major version is the leading number before dot or hyphen
                major := ver
                if idx := strings.IndexAny(ver, ".-"); idx != -1 {
                    major = ver[:idx]
                }
                if major != "" {
                    // Treat non-numeric or parse errors as Berry to be conservative
                    // because Yarn 1 rarely sets packageManager.
                    if m := major; m >= "2" {
                        return true
                    }
                }
                // If we couldn't confidently parse, still assume Berry since packageManager exists.
                return true
            }
        }
    }

    return false
}
