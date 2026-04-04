// .commitlintrc.cjs — Conventional commit rules for Digits
module.exports = {
  extends: ['@commitlint/config-conventional'],
  ignores: [(message) => /^Merge\b/.test(message)],
  rules: {
    'scope-enum': [
      2,
      'always',
      ['pi', 'digitsd', 'firmware', 'server', 'image', 'docs', 'ci'],
    ],
    'scope-empty': [1, 'never'],
    'subject-case': [0],
  },
};
