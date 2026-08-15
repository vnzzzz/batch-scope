# Skills

BatchScope repositoryの`skills/`は、製品利用者へ配布するPublic Skillだけを管理します。

```text
skills/
├── README.md
└── public/
    └── batchscope/
```

`skills/public/batchscope`は、ジョブ定義からのスナップショット作成、取込、検索API利用を支援するBatchScopeの製品成果物です。
repository外へコピーしても利用できる内容とし、GitHub Release archiveへ自己完結する形で同梱します。

BatchScope固有の開発運用はSkillとして管理しません。
共通の実装原則は`AGENTS.md`、Claude Code固有の役割は`CLAUDE.md`、Issue / Pull Request運用は`CONTRIBUTING.md`、再現可能な開発手順は`docs/development/`を正本とします。

複数repositoryで再利用する汎用Skillは`vnzzzz/agent-skills`をCodex / Claude Code PluginとしてDev Containerへ導入して利用します。
BatchScope側ではPlugin内の個別Skill名、Skill一覧、provider repository内部pathを管理しません。
