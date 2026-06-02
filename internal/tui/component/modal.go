package component

// Modal frames a modal screen: it composites content centered over background
// and returns the merged frame, so the cockpit stays visible around the box
// rather than being cleared. It is a thin named wrapper over PlaceOverlay,
// giving the root one vocabulary for floating a confirm or config screen over
// the session list while producing the same output as a direct overlay.
func Modal(content, background string) string {
	return PlaceOverlay(content, background)
}
