#!/bin/sh
# get-versions.sh - Detects versions for dual release channel system
#
# This script:
# 1. Gets the latest production version from releases branch tags
# 2. Generates a beta version with timestamp
# 3. Outputs both versions as JSON

set -eu

# Configuration
RELEASES_BRANCH="origin/releases"
TAG_PATTERN="v[0-9]*"

# Default fallback version (used when no tags found)
DEFAULT_VERSION="v0.0.0"

# Get current UTC timestamp for beta version
# Format: YYYYMMDD-HHMMSS
get_timestamp() {
    date -u +"%Y%m%d-%H%M%S"
}

# Get the latest production version from releases branch
get_prod_version() {
    # Fetch tags from the releases branch
    git fetch "${RELEASES_BRANCH}" 2>/dev/null || true

    # Get the latest tag matching the pattern
    # Sort versions in descending order and take the first one
    latest_tag=$(git tag -l "${TAG_PATTERN}" --sort=-version:refname | head -n 1)

    # If no tags found, use default
    if [ -z "${latest_tag}" ]; then
        echo "${DEFAULT_VERSION}"
    else
        echo "${latest_tag}"
    fi
}

# Generate beta version from production version
generate_beta_version() {
    prod_version="$1"
    timestamp="$2"
    echo "${prod_version}-beta-${timestamp}"
}

# Output JSON
output_json() {
    prod_version="$1"
    beta_version="$2"
    printf '{\n  "prod_version": "%s",\n  "beta_version": "%s"\n}\n' "${prod_version}" "${beta_version}"
}

# Main execution
main() {
    prod_version=$(get_prod_version)
    timestamp=$(get_timestamp)
    beta_version=$(generate_beta_version "${prod_version}" "${timestamp}")

    output_json "${prod_version}" "${beta_version}"
}

main
