#!/usr/bin/env bash
###
 # @Author: Cloudflying
 # @Date: 2026-05-12 20:30:10
 # @LastEditTime: 2026-05-12 20:30:11
 # @LastEditors: Cloudflying
 # @Description: Build Release
###

VERSION="v0.0.1"
go mod download
go build -ldflags "-s -w -X 'new-api/common.Version=$VERSION' -extldflags '-static'" -o new-api