#!/usr/bin/env bash
#
# Download RPM packages from a GitHub release, sign them with GPG,
# re-upload the signed packages to the release, and push to Nexus.
#
# Prerequisites:
#   - gh (GitHub CLI, authenticated)
#   - rpm (with --addsign support)
#   - GPG key imported in your keyring
#   - curl
#
# Usage:
#   ./scripts/sign-and-publish-rpm.sh [options] <tag>
#
# Options:
#   --skip-verify          Skip Sigstore attestation verification
#   --gpg-name <name>      GPG key name for rpm --addsign
#   --nexus-url <url>      Nexus base URL       (or env NEXUS_URL)
#   --nexus-repo <name>    YUM hosted repo name  (or env NEXUS_YUM_REPO)
#   --nexus-user <user>    Nexus upload user     (or env NEXUS_USERNAME)
#   --nexus-pass <pass>    Nexus upload password (or env NEXUS_PASSWORD)

set -euo pipefail

SKIP_VERIFY=false
GPG_NAME=""
TAG=""

while [ $# -gt 0 ]; do
    case "$1" in
        --skip-verify)   SKIP_VERIFY=true; shift ;;
        --gpg-name)      GPG_NAME="$2"; shift 2 ;;
        --nexus-url)     NEXUS_URL="$2"; shift 2 ;;
        --nexus-repo)    NEXUS_YUM_REPO="$2"; shift 2 ;;
        --nexus-user)    NEXUS_USERNAME="$2"; shift 2 ;;
        --nexus-pass)    NEXUS_PASSWORD="$2"; shift 2 ;;
        -*)              echo "Unknown option: $1" >&2; exit 1 ;;
        *)
            if [ -z "$TAG" ]; then
                TAG="$1"; shift
            else
                echo "Unexpected argument: $1" >&2; exit 1
            fi
            ;;
    esac
done

if [ -z "$TAG" ]; then
    echo "Usage: $0 [options] <tag>" >&2
    echo "Run with --help or see script header for details." >&2
    exit 1
fi

: "${NEXUS_URL:?Set NEXUS_URL or pass --nexus-url}"
: "${NEXUS_YUM_REPO:?Set NEXUS_YUM_REPO or pass --nexus-repo}"
: "${NEXUS_USERNAME:?Set NEXUS_USERNAME or pass --nexus-user}"
: "${NEXUS_PASSWORD:?Set NEXUS_PASSWORD or pass --nexus-pass}"

REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "==> Downloading .rpm assets from release ${TAG}..."
gh release download "$TAG" --repo "$REPO" --pattern "*.rpm" --dir "$WORKDIR"

rpms=("$WORKDIR"/*.rpm)
if [ ${#rpms[@]} -eq 0 ] || [ ! -f "${rpms[0]}" ]; then
    echo "No .rpm files found in release ${TAG}."
    exit 1
fi

echo "==> Found ${#rpms[@]} RPM(s):"
printf "    %s\n" "${rpms[@]##*/}"

if [ "$SKIP_VERIFY" = true ]; then
    echo ""
    echo "==> Skipping attestation verification (--skip-verify)."
else
    echo ""
    echo "==> Verifying build provenance attestations..."
    failed=0
    for rpm_file in "${rpms[@]}"; do
        name="$(basename "$rpm_file")"
        echo "    Verifying ${name}..."
        if ! gh attestation verify "$rpm_file" --repo "$REPO" 2>&1; then
            echo "    FAILED: ${name} did not pass attestation verification."
            failed=1
        fi
    done
    if [ "$failed" -ne 0 ]; then
        echo ""
        echo "ERROR: One or more RPM files failed attestation verification."
        echo "       Aborting — refusing to sign unverified artifacts."
        exit 1
    fi
    echo "    All attestations verified."
fi

echo ""
echo "==> Signing RPM packages..."
if [ -n "$GPG_NAME" ]; then
    rpm --define "%_gpg_name ${GPG_NAME}" --addsign "${rpms[@]}"
else
    rpm --addsign "${rpms[@]}"
fi

echo ""
echo "==> Uploading signed RPMs back to release ${TAG}..."
gh release upload "$TAG" --repo "$REPO" --clobber "${rpms[@]}"

echo ""
echo "==> Pushing signed RPMs to Nexus..."
for rpm_file in "${rpms[@]}"; do
    name="$(basename "$rpm_file")"
    echo "    Uploading ${name}..."
    curl --fail --silent --show-error \
        -u "${NEXUS_USERNAME}:${NEXUS_PASSWORD}" \
        -X POST \
        -F "yum.asset=@${rpm_file}" \
        -F "yum.asset.filename=${name}" \
        "${NEXUS_URL}/service/rest/v1/components?repository=${NEXUS_YUM_REPO}"
done

echo ""
echo "==> Done. ${#rpms[@]} RPM(s) signed and published."
