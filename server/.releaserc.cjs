// Server semantic-release config — runs from server/ working directory
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
    ['@semantic-release/github', { assets: [], successComment: false, failComment: false }],
  ],
};
