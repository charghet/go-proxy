@echo off

go build -o .\build\go-proxy.exe .\proxy.go
SET CGO_ENABLED=0
SET GOOS=linux
SET GOARCH=amd64
go build -o .\build\go-proxy .\proxy.go