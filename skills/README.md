# Skills

## Public

`skills/public`は、BatchScopeの利用者へ配布するSkillです。
公開Skillは、リポジトリ外へコピーしても利用できる内容にします。
リポジトリ固有の開発手順やIssue運用には依存させません。

## Internal

`skills/internal`は、BatchScope固有の開発用Skillだけを管理します。
Internal Skillは公開用成果物へ含めません。
現在はバックログ監査と実装指揮のSkillをClaude Codeが使用します。

複数repositoryで再利用する`readable-code`と`japanese-technical-writing`は、このrepositoryへ複製しません。
public repository `vnzzzz/agent-skills`をCodex / Claude Code PluginとしてDev Containerへ導入し、両Agentで共通利用します。
BatchScope側ではPlugin内の個別Skill名やprovider repository内部pathをdiscovery設定へ列挙しません。
