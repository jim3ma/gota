#!/bin/bash

PACKAGE_NAME=gota
GO=$(which go)
WORK_DIR=$(pwd)
OUTPUT=$(pwd)/bin
GOTA_DIR=$(pwd)
BINARY_SUFFIX=""

if [ x${WORK_DIR##*/} = x"hack" ]; then
  OUTPUT=${WORK_DIR%/*}/bin
  GOTA_DIR=${WORK_DIR%/*}
elif [ x${WORK_DIR##*/} = x"gota" ]; then
  OUTPUT=$(pwd)/bin
  GOTA_DIR=$(pwd)
else
  echo "Error work directory"
  exit 1
fi

COMMIT=`git rev-parse HEAD`
VERSION=`git rev-parse --abbrev-ref HEAD`
sed -i "s/gota_commit/${COMMIT}/" gota.go
sed -i "s/gota_version/${VERSION}/" gota.go

mkdir -p $OUTPUT

OSs=(darwin darwin dragonfly \
freebsd freebsd freebsd linux linux \
linux linux linux linux netbsd \
netbsd openbsd openbsd \
solaris windows windows)

ARCHs=(386 amd64 amd64 \
386 amd64 arm 386 amd64 \
arm arm64 ppc64 ppc64le 386 \
amd64 386 amd64 \
amd64 386 amd64)

length=${#OSs[@]}
length=`expr $length - 1`

for i in $(seq 0 $length)
do
  if [ $OSs[$i] = "windows" ]; then
    BINARY_SUFFIX=".exe"
  fi
  
  echo Building for OS: ${OSs[$i]}, ARCH: ${ARCHs[$i]}
  BINARY=${PACKAGE_NAME}_${OSs[$i]}_${ARCHs[$i]}_${VERSION}${BINARY_SUFFIX}
  CGO_ENABLED=0 GOOS=${OSs[$i]} GOARCH=${ARCHs[$i]} \
    $GO build -o $OUTPUT/$BINARY $GOTA_DIR/gota/main.go
  (cd $OUTPUT; GZIP=-9 tar cvzpf ${BINARY}.tar.gz ${BINARY}; rm ${BINARY})
done

git checkout -- gota.go