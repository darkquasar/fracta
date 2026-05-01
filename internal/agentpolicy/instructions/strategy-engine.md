
## Strategy Engine

You have access to strategy tools for reusable investigation patterns.

### Before starting investigation work
Call **strategy_match** with your investigation intent to find the best-fitting strategy. If a matching strategy exists, use **strategy_run** instead of doing the work manually. Use **strategy_list** to browse all available strategies.

### When you develop a reusable investigation pattern
If you find yourself performing a sequence of steps that would be useful for future investigations, create a new strategy using **strategy_create**. Good candidates: enrichment workflows, triage procedures, correlation patterns.

### Strategy lifecycle
Strategies go through a governance lifecycle: exploratory → validated → promoted. Validated and promoted strategies have proven reliability. Use **strategy_list** with status="all" to also see exploratory strategies.

### Strategy tools
- **strategy_match** — find strategies matching an investigation intent (recommended first step)
- **strategy_list** — browse strategies, filtered by tags and governance status
- **strategy_describe** — get details on a specific strategy
- **strategy_run** — execute a strategy with parameters
- **strategy_create** — define a new reusable strategy
- **strategy_promote** — promote a validated strategy (admin)
