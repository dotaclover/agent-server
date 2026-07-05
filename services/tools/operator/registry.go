package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"go-agent-studio/services/aitypes"
	"strings"
	"unicode/utf8"
)

const (
	maxToolTextChars = 1000
)

// Config holds operator tool configuration.
type Config struct {
	LLMProvider aitypes.LLMProvider
}

// RegisterTools registers all operator-facing tools for content creation.
func RegisterTools(registry *aitypes.ToolRegistry, cfg Config) {
	if cfg.LLMProvider != nil {
		registerOutlineCreator(registry, cfg.LLMProvider)
		registerContentWriter(registry, cfg.LLMProvider)
		registerStyleRefiner(registry, cfg.LLMProvider)
		registerCraftImagePrompt(registry, cfg.LLMProvider)
		registerCraftVideoPrompt(registry, cfg.LLMProvider)
	}
}

// registerOutlineCreator creates writing outlines.
func registerOutlineCreator(registry *aitypes.ToolRegistry, provider aitypes.LLMProvider) {
	registry.Register(&aitypes.Tool{
		Name:        "outline_creator",
		Description: "根据主题创建写作大纲（章节结构、要点）。适用于文章、故事、脚本等内容创作。",
		Parameters: `{
			"type":"object",
			"properties":{
				"topic":{"type":"string","description":"写作主题"},
				"type":{"type":"string","enum":["article","story","script"],"description":"类型：article(文章)、story(故事)、script(脚本)"},
				"target_audience":{"type":"string","description":"受众群体，例如：大众、专业人士、儿童、职场新人"},
				"depth":{"type":"string","enum":["popular","deep"],"description":"大纲深度：popular(科普/通俗)、deep(深度研究/专业)"}
			},
			"required":["topic","type"]
		}`,
		Execute: func(ctx context.Context, arguments string) (string, error) {
			var args struct {
				Topic          string `json:"topic"`
				Type           string `json:"type"`
				TargetAudience string `json:"target_audience"`
				Depth          string `json:"depth"`
			}
			if err := json.Unmarshal([]byte(arguments), &args); err != nil {
				return "", err
			}
			topic, err := normalizeRequiredToolText("topic", args.Topic)
			if err != nil {
				return "", err
			}

			systemPrompt := `你是专业的内容大纲策划师。根据主题、类型、目标受众和深度，创建结构清晰、逻辑严密的写作大纲。

要求：
- 文章：标题、引言、3-5个章节、每章节3-5个要点、结尾。如果深度为deep，需要包含具体的理论/数据支持方向；如果为popular，则需要生动案例方向。
- 故事：背景设定、人物设定、情节大纲（起承转合）、结局。针对不同的受众群体调整叙事节奏和复杂度。
- 脚本：场景设定、人物对白框架、镜头说明、时长分配。

输出格式为结构化文本，清晰标注各部分。`

			userPrompt := fmt.Sprintf("主题：%s\n类型：%s\n", topic, args.Type)
			if args.TargetAudience != "" {
				userPrompt += fmt.Sprintf("目标受众：%s\n", args.TargetAudience)
			}
			if args.Depth != "" {
				userPrompt += fmt.Sprintf("大纲深度：%s\n", args.Depth)
			}
			userPrompt += "\n请创建详细的写作大纲。"

			resp, err := provider.Chat(ctx, []aitypes.Message{
				aitypes.NewMessage(aitypes.RoleSystem, systemPrompt),
				aitypes.NewMessage(aitypes.RoleUser, userPrompt),
			}, nil, &aitypes.LLMConfig{Temperature: 0.7, MaxTokens: 2048})
			if err != nil {
				return "", err
			}

			result := map[string]interface{}{
				"topic":           topic,
				"type":            args.Type,
				"target_audience": args.TargetAudience,
				"depth":           args.Depth,
				"outline":         strings.TrimSpace(resp.Content),
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			return string(data), nil
		},
	})
}

// registerContentWriter writes content based on outline.
func registerContentWriter(registry *aitypes.ToolRegistry, provider aitypes.LLMProvider) {
	registry.Register(&aitypes.Tool{
		Name:        "content_writer",
		Description: "根据大纲或指令撰写内容段落。支持多种文体和风格。",
		Parameters: `{
			"type":"object",
			"properties":{
				"section":{"type":"string","description":"要撰写的章节或段落主题"},
				"context":{"type":"string","description":"上下文信息（可选，如前文摘要、大纲）"},
				"style":{"type":"string","description":"写作风格描述（可选，如正式、轻松、文艺）"},
				"format":{"type":"string","enum":["text","dialogue","qa"],"description":"段落格式：text(正文/叙述)、dialogue(对白/对话)、qa(一问一答)"},
				"tone":{"type":"string","description":"写作语气，例如：权威严谨、亲切幽默、感性文艺、科技极客"}
			},
			"required":["section"]
		}`,
		Execute: func(ctx context.Context, arguments string) (string, error) {
			var args struct {
				Section string `json:"section"`
				Context string `json:"context"`
				Style   string `json:"style"`
				Format  string `json:"format"`
				Tone    string `json:"tone"`
			}
			if err := json.Unmarshal([]byte(arguments), &args); err != nil {
				return "", err
			}
			section, err := normalizeRequiredToolText("section", args.Section)
			if err != nil {
				return "", err
			}

			systemPrompt := `你是专业的内容创作者。根据要求撰写高质量的内容段落。

写作原则：
- 结构清晰：开头、主体、结尾
- 内容充实：事实+观点+案例
- 逻辑严密：论证充分
- 格式契合：如果是对白(dialogue)格式，采用角色对话；如果是问答(qa)格式，采用一问一答；如果是正文(text)，采用常规叙事。

根据给定的章节主题、格式、语气 and 上下文，撰写完整的内容段落。`

			userPrompt := fmt.Sprintf("章节主题：%s\n", section)
			if args.Context != "" {
				userPrompt += fmt.Sprintf("上下文：%s\n", args.Context)
			}
			if args.Format != "" {
				userPrompt += fmt.Sprintf("段落格式：%s\n", args.Format)
			}
			if args.Tone != "" {
				userPrompt += fmt.Sprintf("写作语气：%s\n", args.Tone)
			} else if args.Style != "" {
				userPrompt += fmt.Sprintf("风格/语气：%s\n", args.Style)
			}
			userPrompt += "\n请撰写这一章节的完整内容。"

			resp, err := provider.Chat(ctx, []aitypes.Message{
				aitypes.NewMessage(aitypes.RoleSystem, systemPrompt),
				aitypes.NewMessage(aitypes.RoleUser, userPrompt),
			}, nil, &aitypes.LLMConfig{Temperature: 0.8, MaxTokens: 3072})
			if err != nil {
				return "", err
			}

			result := map[string]interface{}{
				"section": section,
				"format":  args.Format,
				"tone":    args.Tone,
				"style":   args.Style,
				"content": strings.TrimSpace(resp.Content),
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			return string(data), nil
		},
	})
}

// registerStyleRefiner polishes and refines content.
func registerStyleRefiner(registry *aitypes.ToolRegistry, provider aitypes.LLMProvider) {
	registry.Register(&aitypes.Tool{
		Name:        "style_refiner",
		Description: "润色优化已有内容，改善文笔、调整风格、增强表达力。",
		Parameters: `{
			"type":"object",
			"properties":{
				"content":{"type":"string","description":"需要润色的原始内容"},
				"goal":{"type":"string","description":"具体润色要求或目标（可选，如：更正式、更生动）"},
				"preset":{"type":"string","enum":["formalize","simplify","storytelling","copywriting"],"description":"风格预设：formalize(正式专业)、simplify(简洁精炼)、storytelling(生动故事化)、copywriting(小红书/种草文案风)"}
			},
			"required":["content"]
		}`,
		Execute: func(ctx context.Context, arguments string) (string, error) {
			var args struct {
				Content string `json:"content"`
				Goal    string `json:"goal"`
				Preset  string `json:"preset"`
			}
			if err := json.Unmarshal([]byte(arguments), &args); err != nil {
				return "", err
			}
			content, err := normalizeRequiredToolText("content", args.Content)
			if err != nil {
				return "", err
			}

			systemPrompt := `你是专业的文字编辑。对给定内容进行润色优化。

根据预设的目标风格进行精细调整：
- formalize (正式化): 转换为书面语、语法严谨、用词专业。
- simplify (简化): 改善精炼度，去除无意义废话，提炼重点。
- storytelling (故事化): 增加画面感、情绪描写和修辞，使其更生动。
- copywriting (文案化): 采用富有感染力的口吻，合理使用段落排版、标签（如有必要），吸引读者眼球。

保持原意，提升表达质量。`

			userPrompt := fmt.Sprintf("原始内容：\n%s\n", content)
			if args.Preset != "" {
				userPrompt += fmt.Sprintf("\n润色风格预设：%s\n", args.Preset)
			}
			if args.Goal != "" {
				userPrompt += fmt.Sprintf("具体润色目标/要求：%s\n", args.Goal)
			}
			userPrompt += "\n请对内容进行润色优化。"

			resp, err := provider.Chat(ctx, []aitypes.Message{
				aitypes.NewMessage(aitypes.RoleSystem, systemPrompt),
				aitypes.NewMessage(aitypes.RoleUser, userPrompt),
			}, nil, &aitypes.LLMConfig{Temperature: 0.6, MaxTokens: 3072})
			if err != nil {
				return "", err
			}

			result := map[string]interface{}{
				"original_length": len(content),
				"refined_length":  len(resp.Content),
				"goal":            args.Goal,
				"preset":          args.Preset,
				"refined_content": strings.TrimSpace(resp.Content),
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			return string(data), nil
		},
	})
}

// registerCraftImagePrompt registers image prompt tool for operator.
func registerCraftImagePrompt(registry *aitypes.ToolRegistry, provider aitypes.LLMProvider) {
	registry.Register(&aitypes.Tool{
		Name:        "craft_image_prompt",
		Description: "帮用户润色扩展为高质量 AI 图片生成 Prompt。用于文章配图、故事插画等。",
		Parameters: `{
			"type":"object",
			"properties":{
				"idea":{"type":"string","description":"用户对画面的想法"},
				"style":{"type":"string","description":"视觉风格"},
				"aspect":{"type":"string","description":"宽高比，如 1:1、9:16、16:9、4:3"}
			},
			"required":["idea"]
		}`,
		Execute: func(ctx context.Context, arguments string) (string, error) {
			var args struct {
				Idea   string `json:"idea"`
				Style  string `json:"style"`
				Aspect string `json:"aspect"`
			}
			if err := json.Unmarshal([]byte(arguments), &args); err != nil {
				return "", err
			}
			idea, err := normalizeRequiredToolText("idea", args.Idea)
			if err != nil {
				return "", err
			}
			if args.Style == "" {
				args.Style = "现代简约，干净明亮，高画质"
			}
			if args.Aspect == "" {
				args.Aspect = "16:9"
			}

			systemPrompt := imagePromptSystemPrompt()
			userPrompt := fmt.Sprintf("根据以下需求，写一个高质量的 AI 图片生成 Prompt：\n想法：%s\n风格：%s\n构图比例：%s", idea, args.Style, args.Aspect)

			resp, err := provider.Chat(ctx, []aitypes.Message{
				aitypes.NewMessage(aitypes.RoleSystem, systemPrompt),
				aitypes.NewMessage(aitypes.RoleUser, userPrompt),
			}, nil, &aitypes.LLMConfig{Temperature: 0.7, MaxTokens: 1024})
			if err != nil {
				return "", err
			}

			result := map[string]interface{}{
				"final_prompt": strings.TrimSpace(resp.Content),
				"style":        args.Style,
				"aspect_ratio": args.Aspect,
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			return string(data), nil
		},
	})
}

// registerCraftVideoPrompt registers video prompt tool for operator.
func registerCraftVideoPrompt(registry *aitypes.ToolRegistry, provider aitypes.LLMProvider) {
	registry.Register(&aitypes.Tool{
		Name:        "craft_video_prompt",
		Description: "帮用户编写 AI 短视频生成 Prompt。输出分镜描述、运镜建议和时长参考。",
		Parameters: `{
			"type":"object",
			"properties":{
				"topic":{"type":"string","description":"视频主题"},
				"style":{"type":"string","description":"视觉风格"},
				"duration":{"type":"integer","description":"目标秒数，默认15"}
			},
			"required":["topic"]
		}`,
		Execute: func(ctx context.Context, arguments string) (string, error) {
			var args struct {
				Topic    string `json:"topic"`
				Style    string `json:"style"`
				Duration int    `json:"duration"`
			}
			if err := json.Unmarshal([]byte(arguments), &args); err != nil {
				return "", err
			}
			topic, err := normalizeRequiredToolText("topic", args.Topic)
			if err != nil {
				return "", err
			}
			if args.Style == "" {
				args.Style = "干净明亮，现代都市，高画质"
			}
			if args.Duration <= 0 {
				args.Duration = 15
			}

			systemPrompt := videoPromptSystemPrompt()
			userPrompt := fmt.Sprintf("根据以下需求，写一个 %d 秒短视频的分镜 prompt：\n主题：%s\n风格：%s", args.Duration, topic, args.Style)

			resp, err := provider.Chat(ctx, []aitypes.Message{
				aitypes.NewMessage(aitypes.RoleSystem, systemPrompt),
				aitypes.NewMessage(aitypes.RoleUser, userPrompt),
			}, nil, &aitypes.LLMConfig{Temperature: 0.7, MaxTokens: 1536})
			if err != nil {
				return "", err
			}

			result := map[string]interface{}{
				"topic":             topic,
				"style":             args.Style,
				"target_duration_s": args.Duration,
				"prompt":            strings.TrimSpace(resp.Content),
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			return string(data), nil
		},
	})
}

func normalizeRequiredToolText(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > maxToolTextChars {
		return "", fmt.Errorf("%s is too long; max %d characters", field, maxToolTextChars)
	}
	return value, nil
}

func imagePromptSystemPrompt() string {
	return `你是 AI 图片生成 Prompt 工程专家。用户会用中文描述想要的画面，你需要输出一段高质量、可直接复制使用的 AI 图片生成 Prompt。

要求：
1. 用英文写 prompt，但保留中文关键专有名词。
2. 包含：主体描述、场景环境、光线氛围、视觉风格、构图比例、画质要求。
3. 直接输出 prompt 文本，不要解释、不要前缀、不要 markdown 标记。
4. Prompt 要具体，避免模糊词（如"好看""漂亮"）`
}

func videoPromptSystemPrompt() string {
	return `你是 AI 短视频生成 Prompt 工程专家。用户会描述视频主题，你需要输出分镜级别的视频生成 Prompt。

要求：
1. 用中文写 prompt，每个分镜用 "---" 分隔。
2. 每个分镜包含：时间段、画面描述、运镜方式、光线氛围。
3. 总时长按照用户要求的秒数均匀分配，每个分镜 3-5 秒。
4. 开头给一个总体风格说明（一行），然后是分镜列表。
5. 直接输出 prompt 文本，不要解释、不要前缀、不要 markdown 标记。`
}
