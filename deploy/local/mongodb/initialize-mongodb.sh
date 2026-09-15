#!/usr/bin/env bash
set -euo pipefail

root_auth=(
  --quiet
  --host mongodb:27017
  --username "$MONGODB_ROOT_USERNAME"
  --password "$MONGODB_ROOT_PASSWORD"
  --authenticationDatabase admin
)

mongosh "${root_auth[@]}" --eval '
try {
  rs.status();
} catch (error) {
  if (error.codeName !== "NotYetInitialized") {
    throw error;
  }
  rs.initiate({
    _id: "record-hub-rs",
    members: [{_id: 0, host: "mongodb:27017"}]
  });
}
'

for attempt in {1..60}; do
  if mongosh "${root_auth[@]}" --eval 'quit(db.hello().isWritablePrimary ? 0 : 1)'; then
    break
  fi
  if [[ "$attempt" == 60 ]]; then
    echo "MongoDB replica set did not elect a primary" >&2
    exit 1
  fi
  sleep 1
done

mongosh "${root_auth[@]}" --eval '
const applicationDB = db.getSiblingDB("record_hub");
const username = process.env.RECORD_HUB_MONGODB_USERNAME;
const password = process.env.RECORD_HUB_MONGODB_PASSWORD;
const roles = [{role: "readWrite", db: "record_hub"}];

if (applicationDB.getUser(username) === null) {
  applicationDB.createUser({user: username, pwd: password, roles});
} else {
  applicationDB.updateUser(username, {pwd: password, roles});
}
'
