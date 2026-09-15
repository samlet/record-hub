#!/usr/bin/env sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output_root=${GENERATED_SDK_DIR:-"$repository_root/build/generated-clients"}
generator_image=${OPENAPI_GENERATOR_IMAGE:-openapitools/openapi-generator-cli:v7.15.0}

case "$output_root" in
  "$repository_root"/*) ;;
  *)
    echo "GENERATED_SDK_DIR must be inside the repository" >&2
    exit 2
    ;;
esac

relative_output=${output_root#"$repository_root"/}
mkdir -p "$output_root"

for destination in go go-server-stub java typescript; do
  rm -rf "$output_root/$destination"
done

generate() {
  language=$1
  destination=$2
  properties=$3
  docker run --rm \
    --user "$(id -u):$(id -g)" \
    --volume "$repository_root:/local" \
    "$generator_image" generate \
    --input-spec /local/api/openapi.yaml \
    --generator-name "$language" \
    --output "/local/$relative_output/$destination" \
    --additional-properties "$properties" \
    --global-property apiDocs=false,modelDocs=false,apiTests=false,modelTests=false
}

generate go go 'packageName=recordhubclient,isGoSubmodule=false'
generate go-server go-server-stub 'packageName=recordhubserver,sourceFolder=go'
generate java java 'artifactId=record-hub-client,groupId=com.samlet.recordhub,invokerPackage=com.samlet.recordhub.client,apiPackage=com.samlet.recordhub.client.api,modelPackage=com.samlet.recordhub.client.model,dateLibrary=java8'
generate typescript-fetch typescript 'npmName=@samlet/record-hub-client,supportsES6=true,typescriptThreePlus=true'

test -f "$output_root/go/client.go"
test -f "$output_root/go-server-stub/go/api.go"
test -f "$output_root/java/pom.xml"
test -f "$output_root/typescript/package.json"
