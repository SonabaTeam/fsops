Usage:
```go
import "github.com/SonabaTeam/fsops"

// FOR COPY
c := fsops.Copy{
	SrcPath: "your source folder path", 
	NewPath: "your new folder path", 
	Fn     : func(err error) { 
		// HERE EXECUTE YOUR CODE WHEN FOLDER COMPLETED COPY
  },
}
c.Submit() // SUBMIT YOUR REQUEST FOR FOLDER COPY

// FOR DELETE
d := fsops.Delete{
	SrcPath: "your source folder path", 
	Fn     : func(err) { 
		// HERE EXECUTE YOUR CODE WHEN FOLDER COMPLETED DELETE
	},
}
d.Submit() // SUBMIT YOUR REQUEST FOR FOLDER DELETE
```