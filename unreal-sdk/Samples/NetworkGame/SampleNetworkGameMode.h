#pragma once

#include "CoreMinimal.h"
#include "GameFramework/GameModeBase.h"
#include "NATTypes.h"
#include "SampleNetworkGameMode.generated.h"

class UNATClient;

/**
 * Sample game mode demonstrating NAT Punchthrough Hero integration.
 * Hosts a game session and handles player connections via NAT traversal.
 */
UCLASS()
class ASampleNetworkGameMode : public AGameModeBase
{
	GENERATED_BODY()

public:
	ASampleNetworkGameMode();

	/** Master server URL. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough Sample")
	FString MasterServerUrl = TEXT("http://localhost:8080");

	/** API key for the master server. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough Sample")
	FString ApiKey;

	/** Name for the hosted game. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough Sample")
	FString GameName = TEXT("My Unreal Game");

	/** Maximum number of players. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough Sample")
	int32 MaxPlayers = 4;

	/** Optional password for the game. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough Sample")
	FString Password;

	/** Host a game on BeginPlay. If false, use JoinGameByCode() instead. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough Sample")
	bool bHostOnStart = true;

	/** Host a new game session. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough Sample")
	void HostNewGame();

	/** Join an existing game by join code. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough Sample")
	void JoinGameByCode(const FString& JoinCode);

	/** Stop the current session. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough Sample")
	void StopSession();

	/** Get the current join code (valid after hosting). */
	UFUNCTION(BlueprintPure, Category = "NAT Punchthrough Sample")
	FString GetJoinCode() const;

protected:
	virtual void BeginPlay() override;
	virtual void EndPlay(const EEndPlayReason::Type EndPlayReason) override;

	UPROPERTY()
	UNATClient* NATClient;

private:
	UFUNCTION()
	void OnGameHosted(const FString& GameId, const FString& JoinCode, const FString& HostToken);

	UFUNCTION()
	void OnConnectionEstablished(const FString& PeerEndpoint);

	UFUNCTION()
	void OnPeerJoined(const FString& PeerId);

	UFUNCTION()
	void OnNATError(const FString& Error);

	UFUNCTION()
	void OnNATTypeDetected(ENATType NATType, const FString& NATTypeName);
};
