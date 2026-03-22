# NAT Punchthrough Hero - Unreal Engine Sample

This sample demonstrates hosting and joining a game using the NAT Punchthrough Hero plugin.

## Files

- **SampleNetworkGameMode** - Game mode that hosts a game session on BeginPlay and displays connection info on screen.
- **SamplePlayerController** - Player controller with `JoinByCode()` to join an existing game via NAT traversal.

## Setup

1. Copy the `NATpunchthrough` plugin to your project's `Plugins/` folder.
2. Enable the plugin in your `.uproject` file or via Edit > Plugins.
3. Set `SampleNetworkGameMode` as your Game Mode in World Settings.
4. Configure `MasterServerUrl` and optionally `ApiKey` in the game mode's details panel.
5. Play in editor — the join code will display on screen.

## Joining from Another Client

Use `SamplePlayerController` or call `JoinByCode` from Blueprints/C++:

```cpp
// From any actor with a reference to the player controller:
ASamplePlayerController* PC = Cast<ASamplePlayerController>(GetWorld()->GetFirstPlayerController());
if (PC)
{
    PC->JoinByCode(TEXT("XK9M2P"));
}
```

## Blueprint Usage

Both `UNATClient` and its sub-components expose all methods and events as `BlueprintCallable`/`BlueprintAssignable`, so you can use them directly in Blueprint graphs without writing C++.
