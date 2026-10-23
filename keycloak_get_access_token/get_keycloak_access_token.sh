# Define your variables
KEYCLOAK_URL="https://localhost:9001"
REALM="mattermost"
source ./set_keycloak_client_secrets.sh

# Request the access token
RESPONSE=$(curl -X POST "$KEYCLOAK_URL/realms/$REALM/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=client_credentials" \
  -d "client_id=$CLIENT_ID" \
  -d "client_secret=$CLIENT_SECRET")

echo $(echo $RESPONSE | jq -r '.access_token') > ../.keycloak-admin-token