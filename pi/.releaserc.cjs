// Pi (digitsd) semantic-release config — runs from repo root
const isGitea = !!process.env.GITEA_URL;

const publishPlugin = isGitea
  ? ['@saithodev/semantic-release-gitea', {
      giteaUrl: process.env.GITEA_URL,
      assets: [
        { path: 'artifacts/digitsd-*-aarch64', label: 'digitsd (aarch64 Linux)' },
        { path: 'artifacts/digitsd-*-aarch64.sha256', label: 'digitsd SHA256 checksum' },
      ],
    }]
  : ['@semantic-release/github', {
      assets: [
        { path: 'artifacts/digitsd-*-aarch64', label: 'digitsd (aarch64 Linux)' },
        { path: 'artifacts/digitsd-*-aarch64.sha256', label: 'digitsd SHA256 checksum' },
      ],
      successComment: false,
      failComment: false,
    }];

module.exports = {
  branches: ['main'],
  tagFormat: 'pi/v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', {
      releaseRules: [
        { scope: 'pi', type: 'feat', release: 'minor' },
        { scope: 'pi', type: 'fix', release: 'patch' },
        { scope: 'pi', type: 'perf', release: 'patch' },
        { scope: 'pi', breaking: true, release: 'major' },
        { scope: '!pi', release: false },
      ],
      parserOpts: {
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
    }],
    '@semantic-release/release-notes-generator',
    ['@semantic-release/exec', {
      prepareCmd: 'bash tools/build-pi.sh ${nextRelease.version}',
    }],
    publishPlugin,
  ],
};
