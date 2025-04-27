#!/bin/bash

grep 'RELEASE_VERSION[[:space:]]*=' version.go  | awk -F= '{print $2}' | sed -e 's_"__g' -e 's/[[:space:]]//g'
