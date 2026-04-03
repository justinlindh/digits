// .commitlintrc.cjs — Conventional commit rules for Digits
module.exports = {
  extends: ['@commitlint/config-conventional'],
  rules: {
    'scope-enum': [
      2,
      'always',
      ['pi', 'firmware', 'server', 'image', 'docs', 'ci'],
    ],
    'scope-empty': [1, 'never'],
  },
};
