// Server semantic-release config — runs from server/ working directory
module.exports = {
  branches: ['main'],
  tagFormat: 'server/v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', {
      releaseRules: [
        { scope: 'server', type: 'feat', release: 'minor' },
        { scope: 'server', type: 'fix', release: 'patch' },
        { scope: 'server', type: 'perf', release: 'patch' },
        { scope: 'server', breaking: true, release: 'major' },
        { scope: '!server', release: false },
      ],
      parserOpts: {
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
    }],
    ['@semantic-release/release-notes-generator', {
      parserOpts: {
        headerPattern: /^(\w*)(?:\((server)\))?: (.*)$/,
        headerCorrespondence: ['type', 'scope', 'subject'],
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
    }],
    ['@semantic-release/github', { assets: [], successComment: false, failComment: false }],
  ],
};
