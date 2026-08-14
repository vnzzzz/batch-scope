# Skills

## Public

`skills/public`は、BatchScopeの利用者へ配布するSkillです。
公開Skillは、リポジトリ外へコピーしても利用できる内容にします。
リポジトリ固有の開発手順やIssue運用には依存させません。

## Internal

`skills/internal`は、BatchScope固有の開発用Skillだけを管理します。
Internal Skillは公開用成果物へ含めません。
バックログ監査と実装指揮のSkillはClaude Codeだけが使用します。

repository非依存の汎用Skillは`vnzzzz/agent-skills` Pluginから利用し、このdirectoryへ複製しません。
BatchScopeはPlugin内の個別Skill名や内部pathを管理しません。
