-- Comments the configured greeting on each task moved into in-review.
function handle(event, docket)
    local greeting = docket.plugin.config.greeting or "Hello"
    docket.task.comment(event.task, greeting .. " from hello-hooks")
end
