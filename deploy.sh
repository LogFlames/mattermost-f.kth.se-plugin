nv=$(node --version)
if [ $nv != "v16.16.0" ]; then
    echo "Please use node v16.16.0"
    exit 1
fi

server_ssh_name="${MM_SERVER_SSH_NAME:-fmm}"

old_size=$(echo "/opt/mattermost/bin/mmctl config get FileSettings.MaxFileSize --local" | ssh "$server_ssh_name" "/bin/bash")
echo "Saved original value of max file upload: $old_size"

echo "/opt/mattermost/bin/mmctl config set FileSettings.MaxFileSize 268435456 --local" | ssh "$server_ssh_name" "/bin/bash"

source ./set_secret.sh
MM_SERVICESETTINGS_SITEURL="https://mattermost.fysiksektionen.se" make deploy

echo "Restoring original value of max file upload: $old_size"
echo "/opt/mattermost/bin/mmctl config set FileSettings.MaxFileSize $old_size --local" | ssh "$server_ssh_name" "/bin/bash"
