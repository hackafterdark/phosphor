# Ruby example for structural search testing.

# Config class holds application configuration.
class Config
  attr_accessor :host, :port, :debug

  def initialize
    @host = 'localhost'
    @port = 8080
    @debug = false
  end

  def url
    "http://#{@host}:#{@port}"
  end
end

# Person represents a person.
class Person
  attr_reader :name, :age

  def initialize(name, age)
    @name = name
    @age = age
  end

  def greet
    "Hello, I'm #{@name} and I'm #{@age} years old."
  end
end

# Logger provides logging functionality.
class Logger
  def self.info(message)
    puts "[INFO] #{message}"
  end

  def self.error(message)
    puts "[ERROR] #{message}"
  end
end

# PersonService manages persons.
class PersonService
  def initialize
    @persons = []
  end

  def add_person(name, age)
    person = Person.new(name, age)
    @persons << person
    Logger.info("Added person: #{name}")
    person
  end

  def all_persons
    @persons
  end

  def person_count
    @persons.size
  end
end

# Main execution.
config = Config.new
Logger.info("Starting application at #{config.url}")

service = PersonService.new
service.add_person('Alice', 30)
service.add_person('Bob', 25)
service.add_person('Charlie', 35)

service.all_persons.each do |person|
  puts person.greet
end

puts "Total persons: #{service.person_count}"
