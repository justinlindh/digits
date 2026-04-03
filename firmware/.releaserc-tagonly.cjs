// Firmware semantic-release config — tag-only (no build toolchain available)
const isGitea = !!process.env.GITEA_URL;

const publishPlugin = isGitea
  ? ['@saithodev/semantic-release-gitea', {
      giteaUrl: process.env.GITEA_URL,
      assets: [],
    }]
  : ['@semantic-release/github', { assets: [] }];

module.exports = {
  branches: ['main'],
  tagFormat: 'fw/v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', {
      releaseRules: [
        { scope: 'firmware', type: 'feat', release: 'minor' },
        { scope: 'firmware', type: 'fix', release: 'patch' },
        { scope: 'firmware', type: 'perf', release: 'patch' },
        { scope: 'firmware', breaking: true, release: 'major' },
        { scope: '!firmware', release: false },
      ],
    }],
    '@semantic-release/release-notes-generator',
    publishPlugin,
  ],
};
