package yarninstall

import (
    "encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/paketo-buildpacks/libnodejs"
	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/fs"
)

type BuildPlanMetadata struct {
	Version       string `toml:"version"`
	VersionSource string `toml:"version-source"`
	Build         bool   `toml:"build"`
}

func Detect() packit.DetectFunc {
	return func(context packit.DetectContext) (packit.DetectResult, error) {
		projectPath, err := libnodejs.FindProjectPath(context.WorkingDir)
		if err != nil {
			return packit.DetectResult{}, err
		}

		exists, err := fs.Exists(filepath.Join(projectPath, "yarn.lock"))
		if err != nil {
			return packit.DetectResult{}, err
		}

		if !exists {
			return packit.DetectResult{}, packit.Fail.WithMessage("no 'yarn.lock' file found in the project path %s", projectPath)
		}

		pkg, err := libnodejs.ParsePackageJSON(projectPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return packit.DetectResult{}, packit.Fail.WithMessage("no 'package.json' found in project path %s", filepath.Join(projectPath))
			}

			return packit.DetectResult{}, err
		}
		nodeVersion := pkg.GetVersion()
        // Determine yarn version from package.json (engines.yarn or packageManager)
        yarnVersion := ""
        yarnVersionSource := ""

        // Read raw package.json to inspect engines and packageManager
        pkgJSONPath := filepath.Join(projectPath, "package.json")
        if data, readErr := os.ReadFile(pkgJSONPath); readErr == nil {
            var raw struct {
                Engines        map[string]string `json:"engines"`
                PackageManager string            `json:"packageManager"`
            }
            if jsonErr := json.Unmarshal(data, &raw); jsonErr == nil {
                if raw.Engines != nil {
                    if v, ok := raw.Engines["yarn"]; ok && v != "" {
                        yarnVersion = v
                        yarnVersionSource = "package.json#engines.yarn"
                    }
                }
                if yarnVersion == "" && len(raw.PackageManager) > 0 {
                    // Expect format like "yarn@4.2.2"; extract substring after '@'
                    if idx := len("yarn@"); len(raw.PackageManager) > idx && raw.PackageManager[:idx] == "yarn@" {
                        yarnVersion = raw.PackageManager[idx:]
                        yarnVersionSource = "package.json#packageManager"
                    }
                }
            }
        }

		nodeRequirement := packit.BuildPlanRequirement{
			Name: PlanDependencyNode,
			Metadata: BuildPlanMetadata{
				Build: true,
			},
		}

		if nodeVersion != "" {
			nodeRequirement.Metadata = BuildPlanMetadata{
				Version:       nodeVersion,
				VersionSource: "package.json",
				Build:         true,
			}
		}

        // Compose Yarn requirement, optionally with version metadata
        yarnRequirement := packit.BuildPlanRequirement{
            Name: PlanDependencyYarn,
            Metadata: BuildPlanMetadata{
                Build: true,
            },
        }
        if yarnVersion != "" {
            yarnRequirement.Metadata = BuildPlanMetadata{
                Version:       yarnVersion,
                VersionSource: yarnVersionSource,
                Build:         true,
            }
        }

        return packit.DetectResult{
            Plan: packit.BuildPlan{
                Provides: []packit.BuildPlanProvision{
                    {Name: PlanDependencyNodeModules},
                },
                Requires: []packit.BuildPlanRequirement{
                    nodeRequirement,
                    yarnRequirement,
                },
            },
        }, nil
	}
}
