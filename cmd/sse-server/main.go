package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	http.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Flush headers
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Send SSE events
		eventID := 1
		for {
			select {
			case <-r.Context().Done():
				return
			default:
				// Send an event
				fmt.Fprintf(w, "id: %d\n", eventID)
				fmt.Fprintf(w, "event: message\n")
				fmt.Fprintf(w, "data: Hello from SSE server at %s\n", time.Now().Format(time.RFC3339))
				fmt.Fprintf(w, "\n") // Empty line to end the event

				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}

				eventID++
				time.Sleep(2 * time.Second) // Send event every 2 seconds
			}
		}
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
    <title>SSE Test Server</title>
</head>
<body>
    <h1>SSE Test Server</h1>
    <p>Visit <a href="/events">/events</a> for Server-Sent Events</p>
    <div id="events"></div>

    <script>
        const eventSource = new EventSource('/events');
        const eventsDiv = document.getElementById('events');

        eventSource.onmessage = function(event) {
            const p = document.createElement('p');
            p.textContent = 'Received: ' + event.data;
            eventsDiv.appendChild(p);
        };

        eventSource.onerror = function(event) {
            console.error('SSE error:', event);
        };
    </script>
</body>
</html>
		`)
	})

	fmt.Println("SSE Test Server starting on :8081")
	fmt.Println("Visit http://localhost:8081 for the web interface")
	fmt.Println("Visit http://localhost:8081/events for raw SSE stream")
	log.Fatal(http.ListenAndServe(":8081", nil))
}
