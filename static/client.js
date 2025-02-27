// ...existing code...

function fetchMatchData(matchId) {
    console.log("Fetching match data for ID:", matchId);

    // Add loading indicator
    const teamsContainer = document.getElementById('teamsContainer');
    if (teamsContainer) {
        teamsContainer.innerHTML = '<div class="loading">Loading teams information... Please wait</div>';
    }

    fetch('/faceit/match?matchid=' + matchId)
        .then(response => {
            // Check if response is ok before trying to parse JSON
            if (!response.ok) {
                console.error("Error response from server:", response.status);
                throw new Error(`API returned ${response.status}: ${response.statusText}`);
            }
            return response.json();
        })
        .then(data => {
            console.log("Received match data:", data);

            if (data && data.payload && data.payload.teams) {
                // Update match timestamp
                const matchTimestampElement = document.getElementById('matchTimestamp');
                if (matchTimestampElement && data.payload.startedAt) {
                    matchTimestampElement.textContent = 'Played on ' + new Date(data.payload.startedAt).toLocaleDateString();
                } else if (matchTimestampElement) {
                    matchTimestampElement.textContent = 'Match date not available';
                }

                // Organize players by team
                organizeTeams(data.payload);
            } else {
                // Detailed error for missing data structure
                let errorDetails = "Failed to load match data: ";
                if (!data) errorDetails += "No data received";
                else if (!data.payload) errorDetails += "No payload in response";
                else if (!data.payload.teams) errorDetails += "No teams in payload";

                console.error(errorDetails, data);

                if (teamsContainer) {
                    teamsContainer.innerHTML = '<div class="no-demo">' + errorDetails + '</div>';
                }
            }
        })
        .catch(error => {
            console.error('Error fetching match data:', error);

            if (teamsContainer) {
                teamsContainer.innerHTML = `
                    <div class="no-demo">
                        <p>Error loading match data: ${error.message}</p>
                        <p>Try these troubleshooting steps:</p>
                        <ul style="text-align: left; max-width: 500px; margin: 0 auto;">
                            <li>Check if the match ID is valid</li>
                            <li>The match may no longer be available in Faceit API</li>
                            <li>Try uploading a newer demo</li>
                        </ul>
                    </div>`;
            }
        });
}

// ...existing code...
