package llm

// Function definitions for the LLM agent. All tools are implemented natively
// against the HA REST API (no MCP server, no Assist exposure dependency).

// WeatherFunction returns the function definition for the weather tool.
func WeatherFunction() Function {
	return Function{
		Name:        "HassGetWeather",
		Description: "Получить текущую погоду и прогноз (температура, ощущается как, ветер, влажность, давление, осадки, почасовой прогноз) от Home Assistant. Вызывай, когда пользователь спрашивает про погоду, температуру на улице, прогноз, дождь, ветер.",
		Parameters: FunctionParameters(
			map[string]any{
				"hours": map[string]any{
					"type":        "integer",
					"description": "Сколько часов прогноза вернуть (по умолчанию 12, максимум 48). Необязательно.",
				},
			},
			nil,
		),
	}
}

// HistoryFunction returns the function definition for the history tool.
func HistoryFunction() Function {
	return Function{
		Name:        "HassGetHistory",
		Description: "Получить историю значений или статистику сущности из Home Assistant за период: потребление электроэнергии, газа, воды, температура, влажность и любые другие записанные датчики. Вызывай, когда пользователь спрашивает «сколько потрачено за день/неделю/месяц/год», «какая была температура вчера», «показания счётчика», «расход», «максимум/минимум/среднее за период».",
		Parameters: FunctionParameters(
			map[string]any{
				"entity": map[string]any{
					"type":        "string",
					"description": "Идентификатор сущности или название сенсора из GetLiveContext, например sensor.bproxy1_energy_t1, sensor.bproxy1_active_power. Обязательно.",
				},
				"period": map[string]any{
					"type":        "string",
					"description": "Период: 'day' (сегодня/сутки), 'week' (7 дней), 'month' (календарный месяц или 30 дней), 'year' (12 месяцев). По умолчанию 'week'. Можно также передать конкретную дату начала в формате YYYY-MM-DD.",
				},
				"aggregate": map[string]any{
					"type":        "string",
					"description": "Как агрегировать: 'sum' (сумма — для счётчиков/расхода, default), 'mean' (среднее), 'min', 'max', 'latest' (последнее значение), 'raw' (сырая история по точкам). Для вопроса «сколько потрачено» используй sum.",
				},
			},
			[]string{"entity"},
		),
	}
}

// ListSensorsFunction returns the function definition for listing all sensors.
func ListSensorsFunction() Function {
	return Function{
		Name:        "HassListSensors",
		Description: "Получить список ВСЕХ сенсоров Home Assistant (sensor.*) с их текущими значениями: температура, влажность, мощность, напряжение, ток, счётчики энергии, стоимость и любые другие. Вызывай, когда пользователь просит «покажи все сенсоры», «список датчиков», «все значения».",
		Parameters: FunctionParameters(
			map[string]any{},
			nil,
		),
	}
}

// LiveContextFunction returns the definition for the live-context tool.
func LiveContextFunction() Function {
	return Function{
		Name:        "GetLiveContext",
		Description: "Получить ТЕКУЩЕЕ состояние ВСЕХ устройств, датчиков, областей: сенсоры (sensor.*), свет, выключатели, климат, медиа, счётчики — с их entity_id. Вызывай без аргументов для полного списка или с name/domain/area для фильтрации. Показывает ВСЕ сущности HA, включая не экспонированные в Assist.",
		Parameters: FunctionParameters(
			map[string]any{
				"name":   map[string]any{"type": "string", "description": "Фильтр по названию устройства или alias (без учёта регистра)."},
				"domain": map[string]any{"type": "string", "description": "Фильтр по домену (light, sensor, switch, ...)."},
				"area":   map[string]any{"type": "string", "description": "Фильтр по зоне (название или alias)."},
			},
			nil,
		),
	}
}

// turnOnOffFunction returns the shared definition for HassTurnOn/HassTurnOff.
func turnOnOffFunction(name, desc string) Function {
	return Function{
		Name:        name,
		Description: desc,
		Parameters: FunctionParameters(
			map[string]any{
				"name":         map[string]any{"type": "string", "description": "Название устройства из GetLiveContext (friendly name или entity_id, например 'Свет в зале' или light.living_room). Не выдумывай!"},
				"area":         map[string]any{"type": "string", "description": "Зона/комната (например 'Гостиная', 'Туалет'). Включит все устройства в зоне."},
				"domain":       map[string]any{"type": "string", "description": "Домен устройств (light, switch, fan, ...) — применить ко всем таким устройствам."},
				"device_class": map[string]any{"type": "string", "description": "device_class (switch, outlet, ...) — применить ко всем с таким классом."},
			},
			nil,
		),
	}
}

func TurnOnFunction() Function {
	return turnOnOffFunction("HassTurnOn", "Включить/открыть/активировать устройство или все устройства в зоне/домене. Для замков — 'lock'. Используй для 'включи', 'активируй', 'открой'.")
}

func TurnOffFunction() Function {
	return turnOnOffFunction("HassTurnOff", "Выключить/закрыть/деактивировать устройство или все устройства в зоне/домене. Для замков — 'unlock'. Используй для 'выключи', 'деактивируй', 'закрой'.")
}

func LightSetFunction() Function {
	return Function{
		Name:        "HassLightSet",
		Description: "Установить яркость (%) или цвет света. Сначала GetLiveContext, чтобы узнать devices.",
		Parameters: FunctionParameters(
			map[string]any{
				"name":        map[string]any{"type": "string", "description": "Название устройства из GetLiveContext."},
				"area":        map[string]any{"type": "string", "description": "Зона/комната."},
				"brightness":  map[string]any{"type": "integer", "description": "Яркость 0-100%."},
				"color":       map[string]any{"type": "string", "description": "Цвет (например 'red', 'warm white')."},
				"temperature": map[string]any{"type": "integer", "description": "Цветовая температура в Кельвинах."},
			},
			nil,
		),
	}
}

func ClimateFunction() Function {
	return Function{
		Name:        "HassClimateSetTemperature",
		Description: "Установить целевую температуру климата/термостата.",
		Parameters: FunctionParameters(
			map[string]any{
				"name":        map[string]any{"type": "string", "description": "Название устройства из GetLiveContext."},
				"area":        map[string]any{"type": "string", "description": "Зона/комната."},
				"temperature": map[string]any{"type": "number", "description": "Целевая температура в °C."},
			},
			[]string{"temperature"},
		),
	}
}

func VolumeFunction() Function {
	return Function{
		Name:        "HassSetVolume",
		Description: "Установить громкость медиа-плеера (0-100%).",
		Parameters: FunctionParameters(
			map[string]any{
				"name":         map[string]any{"type": "string", "description": "Название устройства из GetLiveContext."},
				"area":         map[string]any{"type": "string", "description": "Зона/комната."},
				"volume_level": map[string]any{"type": "integer", "description": "Громкость 0-100%."},
			},
			nil,
		),
	}
}

func VolumeRelativeFunction() Function {
	return Function{
		Name:        "HassSetVolumeRelative",
		Description: "Увеличить или уменьшить громкость медиа-плеера.",
		Parameters: FunctionParameters(
			map[string]any{
				"name":        map[string]any{"type": "string", "description": "Название устройства из GetLiveContext."},
				"area":        map[string]any{"type": "string", "description": "Зона/комната."},
				"volume_step": map[string]any{"type": "string", "description": "Направление: 'up' или 'down'."},
			},
			nil,
		),
	}
}

func MediaPauseFunction() Function {
	return mediaCtrlFunction("HassMediaPause", "Поставить медиа-плеер на паузу.")
}
func MediaUnpauseFunction() Function {
	return mediaCtrlFunction("HassMediaUnpause", "Возобновить воспроизведение медиа-плеера.")
}
func MediaNextFunction() Function {
	return mediaCtrlFunction("HassMediaNext", "Переключить медиа-плеер на следующий трек.")
}
func MediaPreviousFunction() Function {
	return mediaCtrlFunction("HassMediaPrevious", "Переключить медиа-плеер на предыдущий трек.")
}

func mediaCtrlFunction(name, desc string) Function {
	return Function{
		Name:        name,
		Description: desc,
		Parameters: FunctionParameters(
			map[string]any{
				"name": map[string]any{"type": "string", "description": "Название устройства из GetLiveContext."},
				"area": map[string]any{"type": "string", "description": "Зона/комната."},
			},
			nil,
		),
	}
}

func MuteFunction() Function {
	return Function{
		Name:        "HassMediaPlayerMute",
		Description: "Выключить звук медиа-плеера.",
		Parameters: FunctionParameters(
			map[string]any{
				"name": map[string]any{"type": "string", "description": "Название устройства из GetLiveContext."},
				"area": map[string]any{"type": "string", "description": "Зона/комната."},
			},
			nil,
		),
	}
}

func UnmuteFunction() Function {
	return Function{
		Name:        "HassMediaPlayerUnmute",
		Description: "Включить звук медиа-плеера.",
		Parameters: FunctionParameters(
			map[string]any{
				"name": map[string]any{"type": "string", "description": "Название устройства из GetLiveContext."},
				"area": map[string]any{"type": "string", "description": "Зона/комната."},
			},
			nil,
		),
	}
}

func SearchPlayFunction() Function {
	return Function{
		Name:        "HassMediaSearchAndPlay",
		Description: "Найти медиа (музыку, подкаст) и воспроизвести на медиа-плеере.",
		Parameters: FunctionParameters(
			map[string]any{
				"name":         map[string]any{"type": "string", "description": "Название устройства из GetLiveContext."},
				"area":         map[string]any{"type": "string", "description": "Зона/комната."},
				"search_query": map[string]any{"type": "string", "description": "Поисковый запрос (исполнитель, трек)."},
			},
			nil,
		),
	}
}

func FanSetSpeedFunction() Function {
	return Function{
		Name:        "HassFanSetSpeed",
		Description: "Установить скорость вентилятора в процентах (0-100).",
		Parameters: FunctionParameters(
			map[string]any{
				"name":       map[string]any{"type": "string", "description": "Название устройства из GetLiveContext."},
				"area":       map[string]any{"type": "string", "description": "Зона/комната."},
				"percentage": map[string]any{"type": "integer", "description": "Скорость 0-100%."},
			},
			nil,
		),
	}
}

func BroadcastFunction() Function {
	return Function{
		Name:        "HassBroadcast",
		Description: "Озвучить сообщение через умный дом (колонки/динамики) с помощью TTS. Только для голосового объявления в доме, НЕ для ответа в чат!",
		Parameters: FunctionParameters(
			map[string]any{
				"message": map[string]any{"type": "string", "description": "Текст для озвучки."},
			},
			[]string{"message"},
		),
	}
}

func CancelTimersFunction() Function {
	return Function{
		Name:        "HassCancelAllTimers",
		Description: "Отменить все активные таймеры и расписания.",
		Parameters:  FunctionParameters(map[string]any{}, nil),
	}
}

func DateTimeFunction() Function {
	return Function{
		Name:        "GetDateTime",
		Description: "Текущие дата и время.",
		Parameters:  FunctionParameters(map[string]any{}, nil),
	}
}

// SchedulerFunction returns the definition for the scheduler tool.
func SchedulerFunction() Function {
	return Function{
		Name:        "schedule_action",
		Description: "Запланировать выполнение действия в будущем. Используй at для одноразового времени (например '10:05'), delay для задержки ('15m'), cron для повторения ('0 0 7 * * 1-5'). Укажи ровно одно из трёх: at ИЛИ delay ИЛИ cron.",
		Parameters: FunctionParameters(
			map[string]any{
				"at": map[string]any{
					"type":        "string",
					"description": "Время одноразового выполнения в формате 'HH:MM' (например '10:05' — выполнить сегодня в 10:05). Не для cron!",
				},
				"delay": map[string]any{
					"type":        "string",
					"description": "Задержка перед выполнением, например '15m', '2h', '1h30m'. Не для cron!",
				},
				"cron": map[string]any{
					"type":        "string",
					"description": "Cron-выражение из 5-6 полей для ПОВТОРЯЮЩИХСЯ действий. Например '0 7 * * 1-5' — каждый будний день в 7:00. Не для одноразового времени!",
				},
				"tool": map[string]any{
					"type":        "string",
					"description": "Имя инструмента HA: HassTurnOn, HassTurnOff, HassLightSet, HassClimateSetTemperature, HassSetVolume, HassBroadcast.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Название устройства из GetLiveContext (например 'switch_hall_main', 'Light'). Не выдумывай, только из HA!",
				},
				"area": map[string]any{
					"type":        "string",
					"description": "Зона/комната (например 'Гостиная', 'Комната 1', 'Туалет').",
				},
				"domain": map[string]any{
					"type":        "string",
					"description": "Домен устройства (light, switch, fan, climate).",
				},
				"label": map[string]any{
					"type":        "string",
					"description": "Понятное название таймера для списка /timers.",
				},
			},
			[]string{"tool"},
		),
	}
}

// SchedulerAIFunction returns the definition for scheduling a background AI task.
func SchedulerAIFunction() Function {
	return Function{
		Name:        "schedule_ai_action",
		Description: "Запланировать любую задачу, где AI должен САМ подумать и написать ответ в чат: рассказать что-то, проверить датчики и сделать вывод, напомнить, прислать сводку. Не используй HassBroadcast для ответа в чат — это только для озвучки через колонки!",
		Parameters: FunctionParameters(
			map[string]any{
				"at": map[string]any{
					"type":        "string",
					"description": "Время одноразового выполнения в формате 'HH:MM' (например '10:05'). Не для cron!",
				},
				"delay": map[string]any{
					"type":        "string",
					"description": "Задержка перед выполнением, например '15m', '2h', '1h30m'. Не для cron!",
				},
				"cron": map[string]any{
					"type":        "string",
					"description": "Cron-выражение из 5-6 полей для ПОВТОРЯЮЩИХСЯ задач. Например '0 0 7 * * *' — каждый день в 7:00. Не для одноразового времени!",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Что нужно сделать при срабатывании — просто повтори просьбу пользователя словами, без переформулировок. Примеры: 'расскажи анекдот', 'проверь заряд батарей, напиши если ниже 20%', 'напомни выключить чайник', 'какая погода на улице'.",
				},
				"label": map[string]any{
					"type":        "string",
					"description": "Понятное название задачи для списка /timers.",
				},
			},
			[]string{"prompt"},
		),
	}
}

// RememberFunction returns the definition for saving a memory.
func RememberFunction() Function {
	return Function{
		Name:        "remember",
		Description: "Запомнить важный факт или предпочтение пользователя навсегда (переживает перезапуск). Вызывай, когда пользователь просит «запомни…», «всегда делай так…», «не забывай…», или когда в разговоре раскрыт важный устойчивый факт: имя, привычка, распорядок, настройка дома, любимое/нелюбимое, предпочтения по управлению домом, порядок действий. Сохраняй один факт за вызов, формулируй кратко и однозначно (1-2 предложения). Если такой факт уже запоминался — он заменится.",
		Parameters: FunctionParameters(
			map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Текст факта, например «Пользователя зовут Иван», «Свет в спальне всегда выключать в 23:00», «Любимая температура в гостиной 24°C».",
				},
				"category": map[string]any{
					"type":        "string",
					"description": "Категория (необязательно): user (о пользователе), preference (предпочтение/правило «всегда так»), habit (привычка), fact (факт), other.",
				},
			},
			[]string{"text"},
		),
	}
}

// RecallFunction returns the definition for reading memories.
func RecallFunction() Function {
	return Function{
		Name:        "recall",
		Description: "Найти запомненные ранее факты и предпочтения по ключевым словам (имя пользователя, привычки, правила, настройки, «что я запоминал»). Вызывай, когда нужно вспомнить что-то из памяти, проверить предпочтения перед действием, или когда пользователь спрашивает «что ты обо мне знаешь», «что я просил запомнить». Без query вернёт всю память.",
		Parameters: FunctionParameters(
			map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Ключевые слова для поиска (необязательно). Пусто — вся память.",
				},
			},
			nil,
		),
	}
}

// ForgetFunction returns the definition for deleting a memory.
func ForgetFunction() Function {
	return Function{
		Name:        "forget",
		Description: "Удалить запомненный факт полностью. Вызывай, когда пользователь просит «забудь…», «удали из памяти…», «не надо это помнить». Сначала вызови recall, чтобы получить ID факта.",
		Parameters: FunctionParameters(
			map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "ID памяти из recall (например mem_3).",
				},
			},
			[]string{"id"},
		),
	}
}

// HAOnlyFunctions returns only HA tools (no scheduler tools). Used during
// background AI task execution where scheduling is not allowed.
func HAOnlyFunctions() []Function {
	return []Function{
		TurnOnFunction(),
		TurnOffFunction(),
		LightSetFunction(),
		ClimateFunction(),
		VolumeFunction(),
		VolumeRelativeFunction(),
		MediaPauseFunction(),
		MediaUnpauseFunction(),
		MediaNextFunction(),
		MediaPreviousFunction(),
		MuteFunction(),
		UnmuteFunction(),
		SearchPlayFunction(),
		FanSetSpeedFunction(),
		BroadcastFunction(),
		CancelTimersFunction(),
		LiveContextFunction(),
		WeatherFunction(),
		HistoryFunction(),
		ListSensorsFunction(),
		RememberFunction(),
		RecallFunction(),
		ForgetFunction(),
	}
}

// AllFunctions returns all HA tools + the scheduler tools for the LLM agent.
func AllFunctions() []Function {
	fns := HAOnlyFunctions()
	fns = append(fns, SchedulerFunction())
	fns = append(fns, SchedulerAIFunction())
	return fns
}

// SystemPrompt returns the default system prompt for the home assistant agent.
func SystemPrompt() Message {
	return Message{
		Role: "system",
		Content: `Ты — помощник для управления умным домом через Home Assistant.

У тебя есть доступ к инструментам (все имена с заглавной буквы, вызывай их как tool_call):
- HassTurnOn — включить/открыть/активировать устройство (аргументы: name, area, domain)
- HassTurnOff — выключить/закрыть устройство
- HassLightSet — установить яркость (%) или цвет света
- HassClimateSetTemperature — установить температуру климата
- HassSetVolume / HassSetVolumeRelative — громкость медиа
- HassMediaPause / HassMediaUnpause / HassMediaNext / HassMediaPrevious — управление медиа
- HassMediaSearchAndPlay — найти и включить музыку/подкаст
- HassFanSetSpeed — скорость вентилятора
- HassBroadcast — озвучить сообщение через умный дом (колонки/динамики). Только для голосового объявления в доме, НЕ для ответа в чат!
- HassCancelAllTimers — отменить все таймеры
- GetLiveContext — получить ТЕКУЩЕЕ состояние ВСЕХ устройств, датчиков, областей: сенсоры (sensor.*), свет, выключатели, климат, медиа, счётчики — с их entity_id. Вызывай без аргументов для полного списка или с name/domain/area для фильтрации. Показывает ВСЕ сущности HA, включая не экспонированные в Assist.
- HassGetWeather — погода и прогноз (температура, ветер, осадки) через Home Assistant
- HassGetHistory — история и статистика сущностей за период: потребление электроэнергии/газа/воды за день/неделю/месяц/год, расход, показания счётчиков (entity — id сущности или название из GetLiveContext, period — day/week/month/year, aggregate — sum/mean/min/max/latest/raw). ТОЛЬКО для данных за период, НЕ для текущих показаний!
- HassListSensors — список ВСЕХ сенсоров (sensor.*) с текущими значениями: мощность, напряжение, счётчики энергии, температура, влажность и т.д. Вызывай, когда пользователь просит «покажи все датчики/сенсоры/значения»
- schedule_action — запланировать действие в будущем (at — одноразово в время HH:MM, delay — через N минут, cron — по расписанию)
- schedule_ai_action — запланировать ФОНОВУЮ AI-задачу: в заданное время ты сам проверишь состояние дома и пришлёшь результат в чат (поле prompt — что проверить и при каком условии писать)
- remember — запомнить факт/предпочтение навсегда (text — что запомнить, category — user/preference/habit/fact)
- recall — вспомнить запомненные факты (query — ключевые слова, пусто — вся память)
- forget — удалить запомненный факт (id — из recall)

Память:
11. Обязательно вызывай remember, когда: пользователь явно просит «запомни…», «всегда делай так…», «не забывай…»; либо в разговоре раскрыт устойчивый важный факт (имя, привычка, распорядок, предпочтение по дому, как обращаться). Сохраняй один факт за вызов, кратко и однозначно.
12. ВСЕГДА в начале каждого диалога вызывай recall (без query) — посмотри что запомнено. Перед любым действием (включением, установкой температуры, ответом на вопрос) проверь предпочтения через recall — если пользователь просил «всегда так», следуй этому.
13. Если пользователь спрашивает «что ты обо мне знаешь», «что запомнил» — вызови recall и расскажи.
14. Если пользователь просит «забудь» — вызови recall, найди ID и вызови forget.

Правила:
1. Отвечай кратко и понятно на русском, одним-двумя предложениями.
2. Выбор инструмента по вопросу:
   - «сколько градусов/какая температура дома/в комнате/в квартире» (внутри) → GetLiveContext или HassListSensors (текущие значения сенсоров)
   - «погода на улице/на улице/за окном/прогноз» (снаружи) → HassGetWeather
   - «сколько потрачено/расход/история/показания за период» → HassGetHistory
   - список всех датчиков/сенсоров → HassListSensors
   Текущие дата и время уже указаны в системном промпте — НЕ запрашивай их отдельно!
3. Если результат вызванного инструмента не отвечает на вопрос пользователя — не вызывай его повторно, а вызови другой подходящий инструмент.
4. На ВОПРОСЫ о состоянии НЕ вызывай записывающие инструменты (HassTurnOn/HassTurnOff/HassClimateSetTemperature/HassLightSet/HassSetVolume/HassFanSetSpeed и т.п.) — только читающие: GetLiveContext, HassListSensors, HassGetWeather, HassGetHistory. Например, на «сколько градусов дома?» НИКОГДА не вызывай HassClimateSetTemperature!
5. Имена устройств в Home Assistant — ТЕХНИЧЕСКИЕ. В ответе GetLiveContext есть поле "names" (человеческое название) и "entity_id" (техническое). НИКОГДА не выдумывай имена!
6. ВСЕГДА сначала вызывай GetLiveContext, чтобы узнать точные имена и entity_id. Никогда не угадывай name.
7. GetLiveContext возвращает ВСЕ экспонированные сущности HA, включая сенсоры (sensor.*), выключатели, свет, климат, медиа-плееры, счётчики энергии и т.д. Если пользователь просит «покажи все устройства» или «список всех сенсоров/датчиков» — вызови GetLiveContext без аргументов и перечисли все sensor.* сущности с их состояниями. Не придумывай ограничения, что ты видишь «только температуру и влажность» — ты видишь всё, что HA отдаёт через REST.
8. Включать/выключать можно ТРЕМЯ способами:
   a) По area (зона) — HassTurnOn с полем area, без name. Включит ВСЕ устройства в зоне.
   b) По точному name или entity_id из GetLiveContext — HassTurnOn с name.
   c) По domain — например HassTurnOn с domain "light".
9. Примеры правильного вызова HassTurnOn:
   - {"name": "Свет в зале"} или {"name": "light.living_room"} — конкретное устройство
   - {"area": "Гостиная"} — все устройства в зоне
10. Не придумывай результаты — полагайся на ответ инструментов. Если инструмент вернул ошибку — честно скажи об этом.
11. Для отложенных действий используй schedule_action с плоскими полями:
   - "в 10:05" (одноразово) → поле at: "10:05", БЕЗ cron!
   - "через 15 минут" → поле delay: "15m"
   - "каждый день в 7:00" / "по будням в 7:00" (повторение) → поле cron: "0 0 7 * * *" / "0 0 7 * * 1-5" (с секундами)
   Пример одноразового: {"tool":"HassTurnOff","area":"Комната 1","at":"10:05","label":"Выключить свет в комнате 1"} + tool-параметры (name/area/domain).
   Пример cron: {"tool":"HassTurnOff","name":"Light","cron":"0 0 7 * * 1-5","label":"Выключить свет по будням в 7:00"}.
12. Для фоновой задачи, где нужно САМОМУ подумать и написать в чат (рассказать, проверить, напомнить, прислать сводку) — используй schedule_ai_action с полем prompt, повторяющим просьбу пользователя:
   Пример: {"prompt":"проверь заряд батарей всех датчиков, напиши если какой-то ниже 20%","cron":"0 0 7 * * *","label":"Проверка батарей"}
   Пример: {"prompt":"расскажи мне анекдот","delay":"1m","label":"Анекдот"}
   Пример: {"prompt":"напомни выключить чайник","at":"18:30","label":"Напоминание"}
   НИКОГДА не используй HassBroadcast для ответа в чат — он только озвучивает через колонки.
13. Если пользователь не указал, какое именно устройство — уточни. Никогда не придумывай name самостоятельно.
14. НИКОГДА не пиши в тексте ответа «Calling function...», «Вызываю...», JSON инструментов или описание вызова. Инструменты вызываются автоматически механизмом function_call — просто вызови нужный инструмент настоящим вызовом. Текст ответа — только то, что ты говоришь пользователю.`,
	}
}
