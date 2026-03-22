#pragma once

#include "CoreMinimal.h"
#include "GameFramework/PlayerController.h"
#include "NATTypes.h"
#include "SamplePlayerController.generated.h"

class UNATClient;

/**
 * Sample player controller that joins an existing game via NAT punchthrough.
 * Demonstrates the client-side join flow.
 */
UCLASS()
class ASamplePlayerController : public APlayerController
{
	GENERATED_BODY()

public:
	ASamplePlayerController();

	/** Master server URL. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough Sample")
	FString MasterServerUrl = TEXT("http://localhost:8080");

	/** API key for the master server. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough Sample")
	FString ApiKey;

	/** Join a game by its join code. Call this from UI or console. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough Sample")
	void JoinByCode(const FString& JoinCode, const FString& Password = TEXT(""));

	/** Disconnect from the current game. */
	UFUNCTION(BlueprintCallable, Category = "NAT Punchthrough Sample")
	void Disconnect();

	/** Get the current connection method as a string. */
	UFUNCTION(BlueprintPure, Category = "NAT Punchthrough Sample")
	FString GetConnectionMethodString() const;

	/** Whether we are currently connected. */
	UFUNCTION(BlueprintPure, Category = "NAT Punchthrough Sample")
	bool IsNATConnected() const;

protected:
	virtual void BeginPlay() override;
	virtual void EndPlay(const EEndPlayReason::Type EndPlayReason) override;

	UPROPERTY()
	UNATClient* NATClient;

private:
	UFUNCTION()
	void OnConnectionEstablished(const FString& PeerEndpoint);

	UFUNCTION()
	void OnConnectionMethodDetermined(EConnectionMethod Method, const FString& MethodName);

	UFUNCTION()
	void OnNATError(const FString& Error);
};
