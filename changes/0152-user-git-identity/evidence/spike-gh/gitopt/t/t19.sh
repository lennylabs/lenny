#!/bin/sh
# t19.sh <flavour>: project mutations gh sends, by name through gh api, because the stub cannot answer gh project's reads in the shape it expects.
. /t/env.sh; . /t/lib.sh
FL=$1; export GIT_CONFIG_GLOBAL=/t/cfg/Tb.gitconfig; TL=1; STEPT=30; cd /tmp
if [ $FL = ghes ]; then export GH_HOST=ghes.example.test GH_ENTERPRISE_TOKEN=lenny-injected; else export GH_TOKEN=lenny-injected; fi
for m in updateProjectV2 deleteProjectV2 addProjectV2ItemById addProjectV2DraftIssue createProjectV2Field updateProjectV2ItemFieldValue archiveProjectV2Item linkProjectV2ToRepository; do step "$FL-$m" "gh api graphql --hostname \${GH_HOST:-github.com} -f query='mutation{$m(input:{}){clientMutationId}}' 2>&1 | cut -c1-100"; done
