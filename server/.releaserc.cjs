// Server semantic-release config — runs from server/ working directory
const isGitea = !!process.env.GITEA_URL;

const publishPlugin = isGitea
  ? ['@saithodev/semantic-release-gitea', {
      giteaUrl: process.env.GITEA_URL,
      assets: [],
    }]
  : ['@semantic-release/github', { assets: [] }];

module.exports = {
  branches: ['main'],
  tagFormat: 'server/v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', {
      releaseRules: [
        { type: 'feat', release: 'minor' },
        { type: 'fix', release: 'patch' },
        { type: 'perf', release: 'patch' },
        { breaking: true, release: 'major' },
      ],
    }],
    '@semantic-release/release-notes-generator',
    publishPlugin,
  ],
};
