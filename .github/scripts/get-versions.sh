#!/bin/sh
# get-versions.sh - Detects versions for dual release channel system
#
# This script:
# 1. Gets the latest production version from releases branch tags
# 2. Generates a beta version with timestamp (industry standard: base on upcoming version + timestamp)
# 3. Outputs both versions as JSON
#
# Version Format:
# - Beta: v{UPCOMING_VERSION}-beta.{YYYYMMDD.HHMMSS}
# - Example: v1.3.0-beta.20260712.143015
# - The timestamp ensures uniqueness even with multiple beta releases in a day

set -eu

# Configuration
RELEASES_BRANCH="origin/releases"
TAG_PATTERN="v[0-9]*"

# Default fallback version (used when no tags found)
DEFAULT_VERSION="v0.0.0"

# Get current UTC timestamp for beta version
# Format: YYYYMMDD.HHMMSS (dot separator for SemVer compatibility)
get_timestamp() {
    date -u +"%Y%m%d.%H%M%S"
}

# Get the latest production version from releases branch
get_prod_version() {
    # Fetch tags from the releases branch
    git fetch "${RELEASES_BRANCH}" 2>/dev/null || true

    # Get the latest tag matching the pattern
    # Sort versions in descending order and take the first one
    latest_tag=$(git tag -l "${TAG_PATTERN}" --sort=-version:refname | head -1)

    # If no tags found, use default
    if [ -z "${latest_tag}" ]; then
        echo "${DEFAULT_VERSION}"
    else
        echo "${latest_tag}"
    fi
}

# Increment minor version for beta (e.g., v1.2.3 → v1.3.0)
increment_minor_version() {
    local prod_version="$1"
    # Parse version components using sed (POSIX compliant)
    major=$(echo "${prod_version}" | sed 's/^v\([0-9]*\)\..*/\1/')
    minor=$(echo "${prod_version}" | sed 's/^v[0-9]*\.\([0-9]*\).*/\1/')
    
    if [ -z "${major}" ] || [ -z "${minor}" ]; then
        echo "${DEFAULT_VERSION}"
        return
    fi
    
    # Increment minor version, reset patch to 0
    new_minor=$((minor + 1))
    echo "v${major}.${new_minor}.0"
}

# Generate beta version based on upcoming version + timestamp
# Industry standard: v{UPCOMING_VERSION}-beta.{TIMESTAMP}
# Example: v1.3.0-beta.20260712.143015
generate_beta_version() {
    upcoming_version=$(increment_minor_version "$1")
    timestamp="$2"
    echo "${upcoming_version}-beta.${timestamp}"
}

# Output JSON
output_json() {
    prod_version="$1"
    upcoming_version=$(increment_minor_version "$prod_version")
    beta_version="$2"
    
    printf '{
  "prod_version": "%s",
  "upcoming_version": "%s",
  "beta_version": "%s"
}
' "${prod_version}" "${upcoming_version}" "${beta_version}"
}

# Main execution
main() {
    prod_version=$(get_prod_version)
    timestamp=$(get_timestamp)
    beta_version=$(generate_beta_version "${prod_version}" "${timestamp}")

    output_json "${prod_version}" "${beta_version}"
}

main
