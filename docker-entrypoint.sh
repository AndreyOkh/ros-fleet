#!/bin/sh
set -e

# If the first argument begins with a dash (e.g. -config /app/config.json), prepend binary
if [ "${1#-}" != "$1" ]; then
    set -- /usr/local/bin/ros-fleet "$@"
fi

# If we are starting ros-fleet
if [ "$1" = "ros-fleet" ] || [ "$1" = "/usr/local/bin/ros-fleet" ]; then
    # When running as root, perform startup ownership checks and drop privileges
    if [ "$(id -u)" = "0" ]; then
        PUID=${PUID:-1000}
        PGID=${PGID:-1000}

        # Dynamically adjust rosfleet UID and GID if PUID/PGID are customized
        CUR_UID=$(id -u rosfleet 2>/dev/null || echo "")
        CUR_GID=$(id -g rosfleet 2>/dev/null || echo "")

        if [ -n "$CUR_UID" ] && [ "$PUID" != "$CUR_UID" ]; then
            sed -i "s/^rosfleet:x:[0-9]*:/rosfleet:x:${PUID}:/" /etc/passwd
        fi
        if [ -n "$CUR_GID" ] && [ "$PGID" != "$CUR_GID" ]; then
            sed -i "s/^rosfleet:x:[0-9]*:/rosfleet:x:${PGID}:/" /etc/group
        fi

        # Ensure required directories exist
        mkdir -p /app/backups/binary /app/backups/exports /home/rosfleet/.ssh

        # Fix ownership of backups directory and subdirectories if needed
        for dir in /app/backups /app/backups/binary /app/backups/exports; do
            if [ -d "$dir" ]; then
                dir_owner=$(stat -c '%u:%g' "$dir" 2>/dev/null || echo "")
                if [ "$dir_owner" != "${PUID}:${PGID}" ]; then
                    chown -R "${PUID}:${PGID}" "$dir"
                fi
            fi
        done

        # Fix ownership of state.json if present as a file and owned by root
        if [ -f /app/state.json ]; then
            state_owner=$(stat -c '%u' /app/state.json 2>/dev/null || echo "")
            if [ "$state_owner" != "$PUID" ]; then
                chown "${PUID}:${PGID}" /app/state.json
            fi
        fi

        # Fix ownership and permissions of home directory
        if [ -d /home/rosfleet ]; then
            home_owner=$(stat -c '%u:%g' /home/rosfleet 2>/dev/null || echo "")
            if [ "$home_owner" != "${PUID}:${PGID}" ]; then
                chown -R "${PUID}:${PGID}" /home/rosfleet
            fi
            chmod 700 /home/rosfleet/.ssh 2>/dev/null || true
        fi

        # Drop privileges and execute as unprivileged user
        exec su-exec "${PUID}:${PGID}" "$@"
    fi
fi

# Fallback for non-root containers (e.g. docker run --user or Kubernetes securityContext)
exec "$@"
