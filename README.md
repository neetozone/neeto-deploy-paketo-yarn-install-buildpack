# Paketo Buildpack for Yarn Install

The Yarn Install CNB generates and provides application dependencies for node
applications that use the [yarn](https://yarnpkg.com) package manager.

**NOTE:** Support for `yarn` is limited to version 1 (Classic).

## Integration

The Yarn Install CNB provides `node_modules` as a dependency. Downstream
buildpacks can require the `node_modules` dependency by generating a [Build
Plan TOML](https://github.com/buildpacks/spec/blob/master/buildpack.md#build-plan-toml)
file that looks like the following:

```toml
[[requires]]

  # The name of the Yarn Install dependency is "node_modules". This value is
  # considered # part of the public API for the buildpack and will not change
  # without a plan # for deprecation.
  name = "node_modules"

  # Note: The version field is unsupported as there is no version for a set of
  # node_modules.

  # The Yarn Install buildpack supports some non-required metadata options.
  [requires.metadata]

    # Setting the build flag to true will ensure that the node modules
    # are available for subsequent buildpacks during their build phase.
    # If you are writing a buildpack that needs a node module during
    # its build process, this flag should be set to true.
    build = true

    # Setting the launch flag to true will ensure that the packages
    # managed by Yarn are available for the running application. If you
    # are writing an application that needs node modules at runtime,
    # this flag should be set to true.
    launch = true

```

## Packaging

To package this buildpack for consumption:

```bash
./scripts/package.sh --version 2.6.6
```

This will build the buildpack for all target architectures specified in `buildpack.toml` (amd64 and arm64 by default) and create a single archive containing binaries for all architectures in the `build/` directory.

This will create a `buildpackage.cnb` file under the `build` directory which you
can use to build your app as follows:
```
pack build <app-name> -p <path-to-app> -b <path/to/node-engine.cnb> -b <path/to/yarn.cnb> \
-b build/buildpackage.cnb
```

## Publishing

To publish this buildpack to ECR:

```bash
# First, authenticate with ECR (if not already authenticated)
aws ecr get-login-password --region us-east-1 | \
  docker login --username AWS --password-stdin 348674388966.dkr.ecr.us-east-1.amazonaws.com

# Then publish the buildpack
./scripts/publish.sh \
  --image-ref 348674388966.dkr.ecr.us-east-1.amazonaws.com/neeto-deploy/paketo/buildpack/yarn-install:<version> \
  --buildpack-type buildpack
```

The script will automatically:
- Read target architectures from `buildpack.toml`
- Extract the buildpack archive
- Publish each architecture separately with arch-suffixed tags (e.g., `yarn-install:<version>-amd64`, `yarn-install:<version>-arm64`)
- Create and push a multi-arch manifest list

## Usage

## Specifying a project path

To specify a project subdirectory to be used as the root of the app, please use
the `BP_NODE_PROJECT_PATH` environment variable at build time either directly
(ex. `pack build my-app --env BP_NODE_PROJECT_PATH=./src/my-app`) or through a
[`project.toml`
file](https://github.com/buildpacks/spec/blob/main/extensions/project-descriptor.md).
This could be useful if your app is a part of a monorepo.

## Run Tests

To run all unit tests, run:
```
./scripts/unit.sh
```

To run all integration tests, run:
```
/scripts/integration.sh
```

## Stack support

For most apps, the Yarn Install Buildpack runs fine on the [Base
builder](https://github.com/paketo-buildpacks/stacks#metadata-for-paketo-buildrun-stack-images).
But when the app requires compilation of native extensions using `node-gyp`,
the buildpack requires that you use the [Full
builder](https://github.com/paketo-buildpacks/stacks#metadata-for-paketo-buildrun-stack-images).
This is because `node-gyp` requires `python` that's absent on the Base builder,
and the module may require other shared objects.
