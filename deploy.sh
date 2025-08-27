old_size=$(echo "/opt/mattermost/bin/mmctl config get FileSettings.MaxFileSize --local" | ssh fmm "/bin/bash")
echo "Saved original value of max file upload: $old_size"

echo "/opt/mattermost/bin/mmctl config set FileSettings.MaxFileSize 268435456 --local" | ssh fmm "/bin/bash"

source ./set_secret.sh
MM_SERVICESETTINGS_SITEURL="https://mattermost.fysiksektionen.se" make deploy

echo "/opt/mattermost/bin/mmctl config set FileSettings.MaxFileSize $old_size --local" | ssh fmm "/bin/bash"
