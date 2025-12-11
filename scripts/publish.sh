#!/usr/bin/env bash

set -eu
set -o pipefail

readonly ROOT_DIR="$(cd "$(dirname "${0}")/.." && pwd)"
readonly BIN_DIR="${ROOT_DIR}/.bin"

# shellcheck source=SCRIPTDIR/.util/tools.sh
source "${ROOT_DIR}/scripts/.util/tools.sh"

# shellcheck source=SCRIPTDIR/.util/print.sh
source "${ROOT_DIR}/scripts/.util/print.sh"

function main {
  local archive_path buildpack_type image_ref token
  token=""

  while [[ "${#}" != 0 ]]; do
    case "${1}" in
    --archive-path | -a)
      archive_path="${2}"
      shift 2
      ;;

    --buildpack-type | -bt)
      buildpack_type="${2}"
      shift 2
      ;;

    --image-ref | -i)
      image_ref="${2}"
      shift 2
      ;;

    --token | -t)
      token="${2}"
      shift 2
      ;;

    --help | -h)
      shift 1
      usage
      exit 0
      ;;

    "")
      # skip if the argument is empty
      shift 1
      ;;

    *)
      util::print::error "unknown argument \"${1}\""
      ;;
    esac
  done

  if [[ -z "${image_ref:-}" ]]; then
    usage
    util::print::error "--image-ref is required"
  fi

  if [[ -z "${buildpack_type:-}" ]]; then
    usage
    util::print::error "--buildpack-type is required"
  fi

  if [[ ${buildpack_type} != "buildpack" && ${buildpack_type} != "extension" ]]; then
    usage
    util::print::error "--buildpack-type accepted values: [\"buildpack\",\"extension\"]"
  fi

  if [[ -z "${archive_path:-}" ]]; then
    util::print::info "Using default archive path: ${ROOT_DIR}/build/buildpack.tgz"
    archive_path="${ROOT_DIR}/build/buildpack.tgz"
  else
    archive_path="${archive_path}"
  fi

  repo::prepare

  tools::install "${token}"

  buildpack::publish "${image_ref}" "${buildpack_type}" "${archive_path}"
}

function usage() {
  cat <<-USAGE
Publishes a buildpack or an extension in to a registry.

OPTIONS
  -a, --archive-path <filepath>       Path to the buildpack or extension arhive (default: ${ROOT_DIR}/build/buildpack.tgz) (optional)
  -h, --help                          Prints the command usage
  -i, --image-ref <ref>               List of image reference to publish to (required)
  -bt --buildpack-type <string>       Type of buildpack to publish (accepted values: buildpack, extension) (required)
  -t, --token <token>                 Token used to download assets from GitHub (e.g. jam, pack, etc) (optional)

USAGE
}

function repo::prepare() {
  util::print::title "Preparing repo..."

  mkdir -p "${BIN_DIR}"

  export PATH="${BIN_DIR}:${PATH}"
}

function tools::install() {
  local token
  token="${1}"

  util::tools::pack::install \
    --directory "${BIN_DIR}" \
    --token "${token}"

  util::tools::yj::install \
    --directory "${BIN_DIR}" \
    --token "${token}"
}

function buildpack::publish() {
  local image_ref buildpack_type archive_path
  image_ref="${1}"
  buildpack_type="${2}"
  archive_path="${3}"

  util::print::title "Publishing ${buildpack_type}..."

  # Read targets from buildpack.toml
  local buildpack_toml="${ROOT_DIR}/buildpack.toml"
  local -a targets=()
  
  if [[ -f "${buildpack_toml}" ]]; then
    util::print::info "Reading targets from ${buildpack_toml}..."
    # Use yj and jq if available
    if command -v yj >/dev/null 2>&1 && command -v jq >/dev/null 2>&1; then
      local targets_json
      targets_json=$(cat "${buildpack_toml}" | yj -tj | jq -r '.targets[]? | "\(.os)/\(.arch)"' 2>/dev/null || echo "")
      while IFS= read -r target; do
        if [[ -n "${target}" ]]; then
          targets+=("${target}")
        fi
      done <<< "${targets_json}"
    fi
  fi

  if [[ ${#targets[@]} -gt 0 ]]; then
    util::print::info "Found ${#targets[@]} target(s) in buildpack.toml: ${targets[*]}"
  fi

  if [[ ! -f "${archive_path}" ]]; then
    util::print::error "buildpack artifact not found at ${archive_path}; run scripts/package.sh first"
  fi

  # For multi-arch, pack needs to publish each architecture separately, then create a manifest
  if [[ ${#targets[@]} -gt 1 ]]; then
    # Multi-arch publishing
    util::print::info "Publishing multi-arch ${buildpack_type} (${#targets[@]} architectures)..."
    
    # Check if manifest list already exists and remove it
    if docker manifest inspect "${image_ref}" >/dev/null 2>&1; then
      util::print::info "Existing manifest list found for ${image_ref}. Removing it..."
      docker manifest rm "${image_ref}"
    fi
    
    # Extract archive once
    local tmp_dir
    tmp_dir=$(mktemp -d -p "${ROOT_DIR}")
    tar -xzf "${archive_path}" -C "${tmp_dir}"
    
    local arch_images=()
    for target in "${targets[@]}"; do
      local arch
      arch=$(echo "${target}" | cut -d'/' -f2)
      local arch_image_ref="${image_ref}-${arch}"

      util::print::info "Publishing ${target} as ${arch_image_ref}..."

  pack \
        ${buildpack_type} package "${arch_image_ref}" \
        --path "${tmp_dir}" \
        --target "${target}" \
    --format image \
    --publish

      arch_images+=("${arch_image_ref}")
    done
    
    # Create multi-arch manifest
    util::print::info "Creating multi-arch manifest for ${image_ref}..."
    docker manifest create "${image_ref}" "${arch_images[@]}"
    docker manifest push "${image_ref}"
    
    rm -rf "${tmp_dir}"
    util::print::info "Successfully published multi-arch ${buildpack_type}: ${image_ref}"
  else
    # Single architecture or no targets detected
    util::print::info "Extracting archive..."
    local tmp_dir
    tmp_dir=$(mktemp -d -p "${ROOT_DIR}")
    tar -xzf "${archive_path}" -C "${tmp_dir}"

    util::print::info "Publishing ${buildpack_type} to ${image_ref}"

    local pack_args=(
      ${buildpack_type} package "${image_ref}"
      --path "${tmp_dir}"
      --format image
      --publish
    )
    
    # Add target if we have one
    if [[ ${#targets[@]} -eq 1 ]]; then
      pack_args+=(--target "${targets[0]}")
    fi

    pack "${pack_args[@]}"
    rm -rf "${tmp_dir}"
  fi
}

main "${@:-}"
