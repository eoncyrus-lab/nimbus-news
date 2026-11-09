package source

import (
	"strings"
	"testing"
)

func TestJin10ScoreSignalVsNoise(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		important int
		wantKeep  bool
	}{
		{
			name:      "PBOC reverse repo",
			text:      "【央行开展5000亿元逆回购操作】金十数据11月9日讯，中标利率1.50%。",
			important: 1,
			wantKeep:  true,
		},
		{
			name:     "Fed speaker rate stance",
			text:     "美联储戴利：目前不应急于下调关键利率，需要更多通胀回落证据。",
			wantKeep: true,
		},
		{
			name:     "ECB rate decision",
			text:     "欧洲央行宣布降息25个基点，存款机制利率降至2.00%。",
			wantKeep: true,
		},
		{
			name:      "US CPI release",
			text:      "美国10月CPI同比上涨3.1%，预期3.0%，前值3.2%。",
			important: 1,
			wantKeep:  true,
		},
		{
			name:      "Eurozone Sentix release",
			text:      "欧元区11月Sentix投资者信心指数 2.4，预期2.1，前值2.7。",
			important: 1,
			wantKeep:  true,
		},
		{
			name:      "MOFCOM policy action",
			text:      "【商务部：继续暂停对韩华海洋5家美国相关子公司实施反制措施2个月】金十数据11月9日讯。",
			important: 1,
			wantKeep:  true,
		},
		{
			name:      "Nvidia premarket move",
			text:      "英伟达(NVDA.O)美股盘前跌逾4%。",
			important: 1,
			wantKeep:  true,
		},
		{
			name:     "China PPI release without important flag",
			text:     "中国10月PPI同比 5.7%，前值 5%。",
			wantKeep: true,
		},
		{
			name:     "post-release bond commentary",
			text:     "非农后美债收益率曲线呈“熊平”，市场聚焦近期加息预期。",
			wantKeep: false,
		},
		{
			name:     "CPI mentioned in market wrap",
			text:     "油价回落带动美债收益率小幅下行，市场等待美国10月CPI。",
			wantKeep: false,
		},
		{
			name:     "Iran precondition statement",
			text:     "伊朗：解除制裁和封锁是开启外交进程的前提。",
			wantKeep: false,
		},
		{
			name:     "negated war escalation",
			text:     "只要谈判有进展 极不可能重返对伊战争 ——特朗普周日表示。",
			wantKeep: false,
		},
		{
			name:     "Egypt central bank inflation",
			text:     "埃及央行：埃及10月核心通胀率同比为13.1%，9月为14.1%。",
			wantKeep: false,
		},
		{
			name:     "Mexico central bank rate talk",
			text:     "据墨西哥媒体El Financiero：墨西哥央行行长表示，央行自9月以来一直在考虑降息。",
			wantKeep: false,
		},
		{
			name:     "Russia central bank rate talk",
			text:     "俄罗斯央行行长纳比乌琳娜：目前我们不应急于下调关键利率。",
			wantKeep: false,
		},
		{
			name:     "ECB succession politics",
			text:     "荷兰财政大臣：欧洲央行行长人选应以能力和资历为依据，而不应成为政治讨论的一部分。",
			wantKeep: false,
		},
		{
			name:     "Iran MFA statement",
			text:     "伊朗外交部发言人巴加埃：国际原子能机构近日访问了布什尔。",
			wantKeep: false,
		},
		{
			name:     "Sentix preview",
			text:     "欧元区11月Sentix投资者信心指数将于十分钟后公布。",
			wantKeep: false,
		},
		{
			name:     "cotton output",
			text:     "【2026年全国棉花产量同比降5.3%至733.7万吨】金十数据11月9日讯。",
			wantKeep: false,
		},
		{
			name:     "LME inventory",
			text:     "伦敦金属交易所(LME)：铜库存增加725吨，镍库存减少306吨。",
			wantKeep: false,
		},
		{
			name:     "analyst forecast",
			text:     "德意志银行：市场焦点转向CPI与PPI，仍预计美联储12月及明年3月各加息25个基点。",
			wantKeep: false,
		},
		{
			name:     "promotional read-more",
			text:     "随着美国金融流动性骤然紧缩，更致命的业绩预期大下修正迎面扑来……点击查看...",
			wantKeep: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := scoreJin10(tc.text, tc.important)
			keep := !result.Drop && result.Score >= 45
			if keep != tc.wantKeep {
				t.Fatalf("keep=%v want %v score=%d reasons=%v text=%q",
					keep, tc.wantKeep, result.Score, result.Reasons, tc.text)
			}
		})
	}
}

func TestJin10ScoreTreatsForeignCentralBanksAsEmerging(t *testing.T) {
	for _, text := range []string{
		"墨西哥央行行长：央行自9月以来一直在考虑降息。",
		"俄罗斯央行行长：目前我们不应急于下调关键利率。",
		"土耳其央行宣布降息250个基点。",
	} {
		if result := scoreJin10(text, 1); !result.Drop && result.Score >= 45 {
			t.Fatalf("foreign central bank should not pass: score=%d reasons=%v text=%q",
				result.Score, result.Reasons, text)
		}
	}
}

func TestJin10NoiseDoesNotSwallowPolicyAnnouncement(t *testing.T) {
	if keyword := jin10NoiseKeyword("商务部公告：决定调整不可靠实体清单措施。", jin10DefaultExclude); keyword != "" {
		t.Fatalf("policy announcement classified as noise: %q", keyword)
	}
	if keyword := jin10NoiseKeyword("某公司公告：股东拟减持不超过2%股份。", jin10DefaultExclude); keyword == "" {
		t.Fatal("stock announcement should be noise")
	}
}

func TestSplitJin10Roundup(t *testing.T) {
	raw := `<span class="section-news">1. 美联储戴利：如有需要准备进一步加息。</span><br/>` +
		`<span class="section-news">2. 特朗普称将行使总统赦免权。</span>`
	parts := splitJin10Roundup(raw, htmlToPlainText(raw))
	if len(parts) != 2 {
		t.Fatalf("parts=%d %v", len(parts), parts)
	}
	if parts[0] != "美联储戴利：如有需要准备进一步加息。" {
		t.Fatalf("unexpected first part %q", parts[0])
	}
	if strings.HasPrefix(parts[1], "2.") {
		t.Fatalf("roundup index not stripped: %q", parts[1])
	}
}
