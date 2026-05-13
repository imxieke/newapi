#!/usr/bin/env bash
###
 # @Author: Cloudflying
 # @Date: 2026-05-12 20:30:10
 # @LastEditTime: 2026-05-14 00:59:05
 # @LastEditors: Cloudflying
 # @Description: Build Release
###

VERSION="v0.0.2"
go mod download

if [[ "$(uname -s)" == 'Darwin' ]]; then
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X 'new-api/common.Version=$VERSION' -extldflags '-static'" -o aiapi
else
  go build -ldflags "-s -w -X 'new-api/common.Version=$VERSION' -extldflags '-static'" -o aiapi
fi
