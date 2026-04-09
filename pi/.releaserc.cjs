// Pi (digitsd) semantic-release config — runs from repo root
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
        { scope: 'digitsd', type: 'feat', release: 'minor' },
        { scope: 'digitsd', type: 'fix', release: 'patch' },
        { scope: 'digitsd', type: 'perf', release: 'patch' },
        { scope: 'digitsd', breaking: true, release: 'major' },
        { scope: '!(pi|digitsd)', release: false },
      ],
      parserOpts: {
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
    }],
    ['@semantic-release/release-notes-generator', {
      parserOpts: {
        headerPattern: /^(\w*)(?:\((pi|digitsd)\))?: (.*)$/,
        headerCorrespondence: ['type', 'scope', 'subject'],
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
    }],
    ['@semantic-release/exec', {
      prepareCmd: 'bash tools/build-pi.sh ${nextRelease.version}',
    }],
    ['@semantic-release/github', {
      assets: [
        { path: 'artifacts/digitsd-*-aarch64', label: 'digitsd (aarch64 Linux)' },
        { path: 'artifacts/digitsd-*-aarch64.sha256', label: 'digitsd SHA256 checksum' },
      ],
      successComment: false,
      failComment: false,
    }],
  ],
};
