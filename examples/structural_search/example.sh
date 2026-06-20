# Bash example for structural search testing.

#!/bin/bash
set -euo pipefail

# Configuration variables.
APP_NAME="phosphor-example"
APP_VERSION="1.0.0"
HOST="localhost"
PORT=8080

# Logging functions.
log_info() {
    echo "[INFO] $1"
}

log_error() {
    echo "[ERROR] $1" >&2
}

# Process a person record.
process_person() {
    local name="$1"
    local age="$2"

    if [ "$age" -lt 0 ] || [ "$age" -gt 150 ]; then
        log_error "Invalid age for $name: $age"
        return 1
    fi

    echo "Processing: $name (age: $age)"
    return 0
}

# Main function.
main() {
    log_info "Starting $APP_NAME v$APP_VERSION"
    log_info "Listening on $HOST:$PORT"

    local persons=("Alice:30" "Bob:25" "Charlie:35")

    for person in "${persons[@]}"; do
        local name="${person%%:*}"
        local age="${person##*:}"

        if process_person "$name" "$age"; then
            log_info "Successfully processed $name"
        fi
    done

    # Count processed persons.
    local count=${#persons[@]}
    echo "Total persons processed: $count"

    log_info "Application complete"
}

# Run main if not sourced.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi