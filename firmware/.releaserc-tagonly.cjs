// firmware/.releaserc-tagonly.cjs — tag-only release (no firmware build)
// Used when the CI runner lacks the Pico SDK / arm toolchain.
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
    ['@saithodev/semantic-release-gitea', {
      giteaUrl: process.env.GITEA_URL,
      assets: [],
    }],
  ],
};
