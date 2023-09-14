#!/bin/bash

docker build . -t urtho/voibot:latest
docker push urtho/voibot:latest
