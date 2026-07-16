from crewai import Agent, Task, Crew

researcher = Agent(role="researcher", goal="find facts")
writer = Agent(role="writer", goal="write it up")
crew = Crew(agents=[researcher, writer], tasks=[])

def go():
    crew.kickoff()
