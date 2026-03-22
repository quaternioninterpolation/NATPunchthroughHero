# Sample: Network Game

Minimal host/join example using the NAT Punchthrough Hero plugin.

## Files

- **SampleNetworkGameMode** — Hosts a game on `BeginPlay`, displays join code on screen.
- **SamplePlayerController** — Client-side join via `JoinByCode()`.

## Setup

1. Install the `NATpunchthrough` plugin in your project's `Plugins/` folder.
2. Set `SampleNetworkGameMode` as your Game Mode in World Settings.
3. Set `MasterServerUrl` (and `ApiKey` if needed) in the game mode's Details panel.
4. Play in editor — the join code appears on screen.

## Joining

From a second client, call `JoinByCode` on the player controller:

```cpp
ASamplePlayerController* PC = Cast<ASamplePlayerController>(GetWorld()->GetFirstPlayerController());
if (PC)
{
    PC->JoinByCode(TEXT("XK9M2P"));
}
```

Both classes expose all methods to Blueprints — no C++ required.
